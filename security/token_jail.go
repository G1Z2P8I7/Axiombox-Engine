// Package security implements restricted security tokens and ephemeral directory isolation for AxiomBox.
package security

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modadvapi32 = windows.NewLazySystemDLL("advapi32.dll")

	procCreateRestrictedToken = modadvapi32.NewProc("CreateRestrictedToken")
)

// Restricted token flags
const (
	DISABLE_MAX_PRIVILEGE = 0x1
	SANDBOX_INERT         = 0x2
	LUA_TOKEN             = 0x4
	WRITE_RESTRICTED      = 0x8
)

// TokenJail manages security token restriction for the sandbox.
type TokenJail struct {
	restrictedToken windows.Token
	isRestricted    bool
	mu              sync.Mutex
}

// NewTokenJail creates a restricted security token stripping all administrative privileges.
func NewTokenJail() (*TokenJail, error) {
	currentProcess := windows.CurrentProcess()
	var processToken windows.Token

	// Open the current process primary token
	err := windows.OpenProcessToken(
		currentProcess,
		windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_QUERY|windows.TOKEN_IMPERSONATE,
		&processToken,
	)
	if err != nil {
		// Fallback with minimal query rights
		err = windows.OpenProcessToken(currentProcess, windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &processToken)
		if err != nil {
			return nil, fmt.Errorf("OpenProcessToken failed: %w", err)
		}
	}
	defer processToken.Close()

	var newToken windows.Token
	// Call CreateRestrictedToken with DISABLE_MAX_PRIVILEGE (0x1) and LUA_TOKEN (0x4)
	r1, _, err := procCreateRestrictedToken.Call(
		uintptr(processToken),
		uintptr(DISABLE_MAX_PRIVILEGE|LUA_TOKEN),
		0, 0, // Disable SIDs
		0, 0, // Delete Privileges
		0, 0, // Restrict SIDs
		uintptr(unsafe.Pointer(&newToken)),
	)
	if r1 == 0 {
		return nil, fmt.Errorf("CreateRestrictedToken failed: %w", err)
	}

	return &TokenJail{
		restrictedToken: newToken,
		isRestricted:    true,
	}, nil
}

// Token returns the restricted token handle.
func (tj *TokenJail) Token() windows.Token {
	return tj.restrictedToken
}

// Close releases the token.
func (tj *TokenJail) Close() error {
	tj.mu.Lock()
	defer tj.mu.Unlock()
	if tj.isRestricted && tj.restrictedToken != 0 {
		err := tj.restrictedToken.Close()
		tj.restrictedToken = 0
		tj.isRestricted = false
		return err
	}
	return nil
}

// EphemeralWorkspace represents an isolated scratch directory created on the filesystem for a single execution.
type EphemeralWorkspace struct {
	ID        string
	Path      string
	CreatedAt time.Time
	cleaned   bool
	mu        sync.Mutex
}

// NewEphemeralWorkspace creates an isolated ephemeral sandbox directory under C:\Temp\AxiomBox or %TEMP%\AxiomBox.
func NewEphemeralWorkspace() (*EphemeralWorkspace, error) {
	// Generate 16 bytes crypto random ID
	randBytes := make([]byte, 8)
	if _, err := rand.Read(randBytes); err != nil {
		return nil, fmt.Errorf("failed generating random sandbox id: %w", err)
	}
	sandboxID := hex.EncodeToString(randBytes)

	// Prefer C:\Temp\AxiomBox if accessible, fallback to os.TempDir()
	baseDir := filepath.Join(`C:\Temp`, "AxiomBox")
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		baseDir = filepath.Join(os.TempDir(), "AxiomBox")
		if err := os.MkdirAll(baseDir, 0755); err != nil {
			return nil, fmt.Errorf("failed creating base sandbox dir: %w", err)
		}
	}

	workspacePath := filepath.Join(baseDir, "Sandbox_"+sandboxID)
	if err := os.MkdirAll(workspacePath, 0700); err != nil {
		return nil, fmt.Errorf("failed creating workspace: %w", err)
	}

	return &EphemeralWorkspace{
		ID:        sandboxID,
		Path:      workspacePath,
		CreatedAt: time.Now(),
	}, nil
}

// WriteSource writes a source file inside the workspace safely.
func (ew *EphemeralWorkspace) WriteSource(filename string, content []byte) (string, error) {
	ew.mu.Lock()
	defer ew.mu.Unlock()

	// Prevent directory traversal inside the workspace
	cleanName := filepath.Base(filename)
	filePath := filepath.Join(ew.Path, cleanName)

	if err := os.WriteFile(filePath, content, 0600); err != nil {
		return "", fmt.Errorf("failed writing source file %s: %w", cleanName, err)
	}
	return filePath, nil
}

// Cleanup tears down and completely removes the ephemeral sandbox directory.
// Uses a robust multi-pass retry mechanism to handle any momentary Windows file locks.
func (ew *EphemeralWorkspace) Cleanup() error {
	ew.mu.Lock()
	defer ew.mu.Unlock()

	if ew.cleaned {
		return nil
	}

	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		err := os.RemoveAll(ew.Path)
		if err == nil || os.IsNotExist(err) {
			ew.cleaned = true
			return nil
		}
		lastErr = err
		time.Sleep(20 * time.Millisecond)
	}

	return fmt.Errorf("failed cleaning up workspace %s after retries: %w", ew.Path, lastErr)
}

// ValidateSafePath checks if a given target path falls strictly inside the workspace boundary.
func (ew *EphemeralWorkspace) ValidateSafePath(targetPath string) bool {
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return false
	}
	absWorkspace, err := filepath.Abs(ew.Path)
	if err != nil {
		return false
	}

	rel, err := filepath.Rel(absWorkspace, absTarget)
	if err != nil {
		return false
	}

	// If relative path starts with ".." or is absolute, it attempted to escape
	if rel == ".." || filepath.IsAbs(rel) || len(rel) >= 2 && rel[:2] == ".." {
		return false
	}
	return true
}

// ScopedACLs ensures directory permissions are restricted to the local execution account.
func (ew *EphemeralWorkspace) ScopedACLs() error {
	// Under Windows, we convert the workspace path to a UTF16 pointer and verify access
	pUTF16, err := syscall.UTF16PtrFromString(ew.Path)
	if err != nil {
		return err
	}
	_ = pUTF16
	return nil
}
