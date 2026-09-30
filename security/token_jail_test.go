package security

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTokenJailCreation(t *testing.T) {
	jail, err := NewTokenJail()
	if err != nil {
		t.Fatalf("Failed to create restricted token jail: %v", err)
	}
	defer jail.Close()

	if jail.Token() == 0 {
		t.Errorf("Expected valid token handle, got 0")
	}
}

func TestEphemeralWorkspaceLifecycle(t *testing.T) {
	ws, err := NewEphemeralWorkspace()
	if err != nil {
		t.Fatalf("Failed to create ephemeral workspace: %v", err)
	}

	// Verify workspace exists on filesystem
	info, err := os.Stat(ws.Path)
	if err != nil || !info.IsDir() {
		t.Fatalf("Expected workspace directory to exist at %s", ws.Path)
	}

	// Write a test source file
	srcPath, err := ws.WriteSource("main.cpp", []byte("int main() { return 0; }"))
	if err != nil {
		t.Fatalf("Failed to write source: %v", err)
	}

	if _, err := os.Stat(srcPath); err != nil {
		t.Fatalf("Source file does not exist at %s", srcPath)
	}

	// Test Path Traversal containment
	traversalEscape := filepath.Join(ws.Path, "..", "..", "Windows", "System32", "calc.exe")
	if ws.ValidateSafePath(traversalEscape) {
		t.Errorf("Path traversal was incorrectly allowed: %s", traversalEscape)
	}

	validInternal := filepath.Join(ws.Path, "subfolder", "file.txt")
	if !ws.ValidateSafePath(validInternal) {
		t.Errorf("Internal path was incorrectly rejected: %s", validInternal)
	}

	// Teardown workspace
	if err := ws.Cleanup(); err != nil {
		t.Fatalf("Failed to cleanup workspace: %v", err)
	}

	// Verify directory is deleted completely
	if _, err := os.Stat(ws.Path); !os.IsNotExist(err) {
		t.Errorf("Workspace directory still exists after cleanup: %s", ws.Path)
	}
}
