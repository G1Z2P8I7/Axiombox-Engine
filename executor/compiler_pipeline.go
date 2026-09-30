// Package executor provides staged compilation and isolated sandboxed execution for C++, Python 3, Node.js, Java, and C#.
package executor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"axiombox/sandbox"
	"axiombox/security"
)

// Language identifiers
const (
	LangCPP        = "cpp"
	LangPython     = "python"
	LangJavaScript = "javascript"
	LangJava       = "java"
	LangCSharp     = "csharp"
)

// PipelineRequest encapsulates a code submission for sandboxed execution.
type PipelineRequest struct {
	Language      string `json:"language"`
	Code          string `json:"code"`
	Stdin         string `json:"stdin"`
	MemoryLimitMB uint64 `json:"memory_limit_mb"` // Default: 64
	TimeLimitMs   uint64 `json:"time_limit_ms"`   // Default: 2000
	CpuRate       uint32 `json:"cpu_rate"`        // Default: 100 (100% of 1 core)
}

// PipelineResult captures compilation and sandboxed execution outputs with kernel telemetry.
type PipelineResult struct {
	Language           string  `json:"language"`
	Status             string  `json:"status"` // COMPLETED, COMPILE_ERROR, TIME_LIMIT_EXCEEDED, MEMORY_LIMIT_EXCEEDED, PROCESS_LIMIT_EXCEEDED, RUNTIME_ERROR
	Stdout             string  `json:"stdout"`
	Stderr             string  `json:"stderr"`
	ExitCode           uint32  `json:"exit_code"`
	CompileTimeMs      int64   `json:"compile_time_ms"`
	ExecutionTimeMs    int64   `json:"execution_time_ms"`
	CpuKernelTimeUs    int64   `json:"cpu_kernel_time_us"`
	CpuUserTimeUs      int64   `json:"cpu_user_time_us"`
	PeakMemoryMB       float64 `json:"peak_memory_mb"`
	ColdStartLatencyMs float64 `json:"cold_start_latency_ms"`
	WorkspaceID        string  `json:"workspace_id"`
}

// NormalizeLanguage standardizes language alias strings.
func NormalizeLanguage(lang string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "cpp", "c++", "cxx", "cc":
		return LangCPP, nil
	case "python", "py", "python3":
		return LangPython, nil
	case "javascript", "js", "node", "nodejs":
		return LangJavaScript, nil
	case "java":
		return LangJava, nil
	case "csharp", "c#", "cs":
		return LangCSharp, nil
	default:
		return "", fmt.Errorf("unsupported language: %s (supported: cpp, python, javascript, java, csharp)", lang)
	}
}

// Execute handles the complete end-to-end lifecycle:
// Ephemeral workspace creation -> Stage 1 Compilation (if applicable) -> Stage 2 Kernel Sandboxed Execution -> Cleanup.
func Execute(req PipelineRequest) (*PipelineResult, error) {
	lang, err := NormalizeLanguage(req.Language)
	if err != nil {
		return nil, err
	}

	// Apply robust defaults
	if req.MemoryLimitMB == 0 {
		req.MemoryLimitMB = 64
	}
	if req.TimeLimitMs == 0 {
		req.TimeLimitMs = 2000
	}
	if req.CpuRate == 0 {
		req.CpuRate = 100
	}

	// 1. Create isolated ephemeral workspace
	ws, err := security.NewEphemeralWorkspace()
	if err != nil {
		return nil, fmt.Errorf("failed creating ephemeral workspace: %w", err)
	}
	// Guarantee cleanup on exit path under any circumstance
	defer ws.Cleanup()

	res := &PipelineResult{
		Language:    lang,
		WorkspaceID: ws.ID,
	}

	memBytes := req.MemoryLimitMB * 1024 * 1024
	// HotSpot JVM runtime requires a minimum memory baseline for classloading metadata
	if lang == LangJava && memBytes < 128*1024*1024 {
		memBytes = 128 * 1024 * 1024
	}

	cfg := sandbox.Config{
		MemoryLimitBytes:   memBytes,
		TimeLimit:          time.Duration(req.TimeLimitMs) * time.Millisecond,
		MaxActiveProcesses: 1, // Kill fork-bombs
		CpuRatePercentage:  req.CpuRate,
		WorkingDir:         ws.Path,
	}

	switch lang {
	case LangCPP:
		return executeCPP(ws, cfg, req, res)
	case LangPython:
		return executePython(ws, cfg, req, res)
	case LangJavaScript:
		return executeJavaScript(ws, cfg, req, res)
	case LangJava:
		return executeJava(ws, cfg, req, res)
	case LangCSharp:
		return executeCSharp(ws, cfg, req, res)
	default:
		return nil, fmt.Errorf("unhandled language: %s", lang)
	}
}

// executeCPP manages staged compilation with GCC and sandboxed execution.
func executeCPP(ws *security.EphemeralWorkspace, cfg sandbox.Config, req PipelineRequest, res *PipelineResult) (*PipelineResult, error) {
	srcPath, err := ws.WriteSource("source.cpp", []byte(req.Code))
	if err != nil {
		return nil, err
	}

	binPath := filepath.Join(ws.Path, "program.exe")

	// STAGE 1: Compilation with 12s deadline and optimization
	compileStart := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "g++", "-O2", "-pipe", "-fno-asm", "-Wall", srcPath, "-o", binPath)
	cmd.Dir = ws.Path
	compileOut, compileErr := cmd.CombinedOutput()
	res.CompileTimeMs = time.Since(compileStart).Milliseconds()

	if compileErr != nil {
		res.Status = "COMPILE_ERROR"
		res.ExitCode = 1
		res.Stderr = string(compileOut)
		if ctx.Err() == context.DeadlineExceeded {
			res.Stderr = "Compilation timed out (exceeded 5.0s limit)\n" + res.Stderr
		}
		return res, nil
	}

	// STAGE 2: Sandboxed Binary Execution inside Windows Job Object
	job, err := sandbox.NewJob(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed creating sandbox job: %w", err)
	}
	defer job.Close()

	execRes, err := job.ExecuteProcess(binPath, req.Stdin)
	if err != nil {
		return nil, fmt.Errorf("sandbox execution error: %w", err)
	}

	populateResult(res, execRes)
	return res, nil
}

// executePython executes Python inside Job Object with runtime hardening.
func executePython(ws *security.EphemeralWorkspace, cfg sandbox.Config, req PipelineRequest, res *PipelineResult) (*PipelineResult, error) {
	// Prepend defensive hardening hooks to block socket and process spawning
	hardeningHeader := `
import sys

# Defensive monkey-patching: disable socket exfiltration and child process spawn
def _blocked(*args, **kwargs):
    raise PermissionError("AxiomBox Security: System process and network primitives are restricted")

try:
    import socket
    socket.socket = _blocked
except Exception:
    pass

try:
    import subprocess
    subprocess.Popen = _blocked
    subprocess.run = _blocked
except Exception:
    pass

# User Code:
`
	fullCode := hardeningHeader + req.Code
	srcPath, err := ws.WriteSource("main.py", []byte(fullCode))
	if err != nil {
		return nil, err
	}

	// Python invocation flags:
	// -u: unbuffered stdout/stderr
	// -B: do not write .pyc files
	// -E: ignore all PYTHON* environment variables
	// -s: do not add user site directory
	cmdLine := fmt.Sprintf("python.exe -u -B -E -s %s", srcPath)

	job, err := sandbox.NewJob(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed creating sandbox job: %w", err)
	}
	defer job.Close()

	execRes, err := job.ExecuteProcess(cmdLine, req.Stdin)
	if err != nil {
		return nil, fmt.Errorf("sandbox execution error: %w", err)
	}

	populateResult(res, execRes)
	return res, nil
}

// executeJavaScript executes Node.js inside Job Object with V8 memory caps and disabled network.
func executeJavaScript(ws *security.EphemeralWorkspace, cfg sandbox.Config, req PipelineRequest, res *PipelineResult) (*PipelineResult, error) {
	hardeningHeader := `
// Defensive sandbox hardening: disable child_process and network modules
const _blocked = () => { throw new Error("AxiomBox Security: Network and process creation are restricted"); };
try {
    const cp = require('child_process');
    cp.spawn = _blocked;
    cp.exec = _blocked;
    cp.fork = _blocked;
} catch(e) {}
try {
    const net = require('net');
    net.connect = _blocked;
    net.createConnection = _blocked;
} catch(e) {}

// User Code:
`
	fullCode := hardeningHeader + req.Code
	srcPath, err := ws.WriteSource("main.js", []byte(fullCode))
	if err != nil {
		return nil, err
	}

	// Node flags:
	// --max-old-space-size=48: V8 heap memory hard cap
	// --no-addons: disables native binary addons
	// --disallow-code-generation-from-strings: disables eval() / new Function() exploits
	cmdLine := fmt.Sprintf("node.exe --max-old-space-size=48 --no-addons --disallow-code-generation-from-strings %s", srcPath)

	job, err := sandbox.NewJob(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed creating sandbox job: %w", err)
	}
	defer job.Close()

	execRes, err := job.ExecuteProcess(cmdLine, req.Stdin)
	if err != nil {
		return nil, fmt.Errorf("sandbox execution error: %w", err)
	}

	populateResult(res, execRes)
	return res, nil
}

// executeJava manages staged compilation with javac and sandboxed JVM execution.
func executeJava(ws *security.EphemeralWorkspace, cfg sandbox.Config, req PipelineRequest, res *PipelineResult) (*PipelineResult, error) {
	cfg.MaxActiveProcesses = 2 // Java launcher architecture spawns JVM host process

	srcPath, err := ws.WriteSource("Main.java", []byte(req.Code))
	if err != nil {
		return nil, err
	}

	// STAGE 1: Compilation with javac
	compileStart := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "javac", "-encoding", "UTF-8", srcPath)
	cmd.Dir = ws.Path
	compileOut, compileErr := cmd.CombinedOutput()
	res.CompileTimeMs = time.Since(compileStart).Milliseconds()

	if compileErr != nil {
		res.Status = "COMPILE_ERROR"
		res.ExitCode = 1
		res.Stderr = string(compileOut)
		if ctx.Err() == context.DeadlineExceeded {
			res.Stderr = "Compilation timed out (exceeded 12.0s limit)\n" + res.Stderr
		}
		return res, nil
	}

	// STAGE 2: Sandboxed JVM Execution inside Job Object
	// Java flags:
	// -Xmx64m: Max heap size 64MB
	// -Xms16m: Initial heap size 16MB
	// -XX:+UseSerialGC: Lightweight single-threaded garbage collection
	// -XX:MaxRAM=96m: Total RAM budget for the JVM
	cmdLine := fmt.Sprintf("java.exe -Xmx64m -Xms16m -XX:+UseSerialGC -XX:MaxRAM=96m -cp \"%s\" Main", ws.Path)

	job, err := sandbox.NewJob(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed creating sandbox job: %w", err)
	}
	defer job.Close()

	execRes, err := job.ExecuteProcess(cmdLine, req.Stdin)
	if err != nil {
		return nil, fmt.Errorf("sandbox execution error: %w", err)
	}

	populateResult(res, execRes)
	return res, nil
}

// executeCSharp manages compilation with Microsoft Visual C# compiler and sandboxed execution.
func executeCSharp(ws *security.EphemeralWorkspace, cfg sandbox.Config, req PipelineRequest, res *PipelineResult) (*PipelineResult, error) {
	srcPath, err := ws.WriteSource("Program.cs", []byte(req.Code))
	if err != nil {
		return nil, err
	}

	binPath := filepath.Join(ws.Path, "Program.exe")

	cscPath := "csc.exe"
	if p, err := exec.LookPath("csc.exe"); err == nil {
		cscPath = p
	} else {
		winCSC := `C:\Windows\Microsoft.NET\Framework64\v4.0.30319\csc.exe`
		if _, err := os.Stat(winCSC); err == nil {
			cscPath = winCSC
		} else {
			cscPath = `C:\Windows\Microsoft.NET\Framework\v4.0.30319\csc.exe`
		}
	}

	// STAGE 1: Compilation with csc
	compileStart := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, cscPath, "/nologo", "/optimize", "/out:"+binPath, srcPath)
	cmd.Dir = ws.Path
	compileOut, compileErr := cmd.CombinedOutput()
	res.CompileTimeMs = time.Since(compileStart).Milliseconds()

	if compileErr != nil {
		res.Status = "COMPILE_ERROR"
		res.ExitCode = 1
		res.Stderr = string(compileOut)
		if ctx.Err() == context.DeadlineExceeded {
			res.Stderr = "Compilation timed out (exceeded 12.0s limit)\n" + res.Stderr
		}
		return res, nil
	}

	// STAGE 2: Sandboxed Binary Execution inside Windows Job Object
	job, err := sandbox.NewJob(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed creating sandbox job: %w", err)
	}
	defer job.Close()

	execRes, err := job.ExecuteProcess(binPath, req.Stdin)
	if err != nil {
		return nil, fmt.Errorf("sandbox execution error: %w", err)
	}

	populateResult(res, execRes)
	return res, nil
}

func populateResult(res *PipelineResult, execRes *sandbox.ExecutionResult) {
	res.Stdout = execRes.Stdout
	res.Stderr = execRes.Stderr
	res.ExitCode = execRes.ExitCode
	res.Status = execRes.Status
	res.ExecutionTimeMs = execRes.ExecutionTime.Milliseconds()
	res.CpuKernelTimeUs = execRes.CpuKernelTimeUs
	res.CpuUserTimeUs = execRes.CpuUserTimeUs
	res.PeakMemoryMB = execRes.PeakMemoryMB
	res.ColdStartLatencyMs = float64(execRes.ColdStartLatency.Microseconds()) / 1000.0
}
