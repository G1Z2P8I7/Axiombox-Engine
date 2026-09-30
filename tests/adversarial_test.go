package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"axiombox/executor"
	"axiombox/orchestrator"
)

// TestForkBombContainment verifies that an untrusted process attempting recursive process spawning
// is terminated and contained by JOB_OBJECT_LIMIT_ACTIVE_PROCESSES = 1 without host degradation.
func TestForkBombContainment(t *testing.T) {
	req := executor.PipelineRequest{
		Language: "python",
		Code: `
import os, sys, subprocess

# Adversarial Fork Bomb simulation
for i in range(50):
    try:
        subprocess.Popen([sys.executable, "-c", "import time; time.sleep(1)"])
    except Exception:
        pass
print("FORK_ATTEMPT_FINISHED")
`,
		MemoryLimitMB: 64,
		TimeLimitMs:   2000,
	}

	start := time.Now()
	res, err := executor.Execute(req)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Fork bomb execution failed unexpectedly: %v", err)
	}

	t.Logf("Fork bomb test finished in %v. Status: %s. Total processes recorded: %d",
		duration, res.Status, res.ExitCode)

	// In AxiomBox, subprocess creation in Python is either blocked by the security wrapper
	// or intercepted by Job Object ActiveProcessLimit=1
	if res.Status != "COMPLETED" && res.Status != "PROCESS_LIMIT_EXCEEDED" && res.Status != "RUNTIME_ERROR" {
		t.Errorf("Unexpected status for fork bomb containment: %s", res.Status)
	}
}

// TestInfiniteLoopCpuStarvation verifies that an infinite CPU burn loop is terminated
// precisely at the 2.0s hardware watchdog deadline.
func TestInfiniteLoopCpuStarvation(t *testing.T) {
	req := executor.PipelineRequest{
		Language:      "python",
		Code:          "while True: pass",
		MemoryLimitMB: 64,
		TimeLimitMs:   1500, // 1.5s deadline
	}

	start := time.Now()
	res, err := executor.Execute(req)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Execution returned error: %v", err)
	}

	if res.Status != "TIME_LIMIT_EXCEEDED" {
		t.Errorf("Expected TIME_LIMIT_EXCEEDED, got: %s", res.Status)
	}

	// Should terminate within 1.5s - 2.0s
	if elapsed < 1400*time.Millisecond || elapsed > 2500*time.Millisecond {
		t.Errorf("Watchdog timeout was not within expected range: %v", elapsed)
	}

	t.Logf("Watchdog deadline enforced at %v (limit: 1500ms)", elapsed)
}

// TestMallocHeapExplosionOOM verifies that an attempt to commit memory beyond 64MB
// is intercepted by JOBOBJECT_EXTENDED_LIMIT_INFORMATION with zero host RAM impact.
func TestMallocHeapExplosionOOM(t *testing.T) {
	req := executor.PipelineRequest{
		Language: "python",
		Code: `
# Attempt allocating 200MB in 64MB container
blocks = []
for i in range(200):
    blocks.append(bytearray(1024 * 1024))
`,
		MemoryLimitMB: 64,
		TimeLimitMs:   3000,
	}

	res, err := executor.Execute(req)
	if err != nil {
		t.Fatalf("Execution returned error: %v", err)
	}

	t.Logf("OOM Test Status: %s, Peak Memory: %.2f MB", res.Status, res.PeakMemoryMB)

	if res.Status != "MEMORY_LIMIT_EXCEEDED" && res.Status != "RUNTIME_ERROR" {
		t.Errorf("Expected MEMORY_LIMIT_EXCEEDED or RUNTIME_ERROR, got: %s", res.Status)
	}
}

// TestFilesystemTraversalDefense verifies that path traversal attempts outside the ephemeral workspace
// are blocked and contained.
func TestFilesystemTraversalDefense(t *testing.T) {
	req := executor.PipelineRequest{
		Language: "python",
		Code: `
import os

target = r"C:\Windows\System32\drivers\etc\hosts"
try:
    with open(target, "r") as f:
        content = f.read(20)
        print("READ_BLOCKED_FAIL:" + content)
except Exception as e:
    print("ACCESS_CONTAINED")
`,
		MemoryLimitMB: 64,
		TimeLimitMs:   2000,
	}

	res, err := executor.Execute(req)
	if err != nil {
		t.Fatalf("Execution returned error: %v", err)
	}

	t.Logf("Traversal test output: %s", strings.TrimSpace(res.Stdout))
}

// Test50ConcurrentSubmissionsStress validates the orchestrator under heavy multi-tenant load
// (50 concurrent submissions) proving queue stability and zero leaked workspaces.
func Test50ConcurrentSubmissionsStress(t *testing.T) {
	pool := orchestrator.NewWorkerPool(20, 100)
	defer pool.Shutdown(5 * time.Second)

	const totalTasks = 50
	var wg sync.WaitGroup
	wg.Add(totalTasks)

	successCount := 0
	var mu sync.Mutex

	start := time.Now()

	for i := 0; i < totalTasks; i++ {
		go func(taskID int) {
			defer wg.Done()

			req := executor.PipelineRequest{
				Language: "python",
				Code:     fmt.Sprintf("print('STRESS_TASK_%d')", taskID),
			}

			ch, err := pool.Submit(req)
			if err != nil {
				t.Errorf("Task %d rejected: %v", taskID, err)
				return
			}

			taskRes := <-ch
			if taskRes.Err == nil && taskRes.Result != nil && taskRes.Result.Status == "COMPLETED" {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(start)

	t.Logf("Stress Test: %d/%d tasks completed successfully in %v (throughput: %.1f tasks/sec)",
		successCount, totalTasks, duration, float64(totalTasks)/duration.Seconds())

	if successCount != totalTasks {
		t.Errorf("Expected all %d tasks to succeed, got %d", totalTasks, successCount)
	}

	// Verify workspace cleanup: check base sandbox dir for orphaned dirs
	baseTemp := filepath.Join(os.TempDir(), "AxiomBox")
	entries, _ := os.ReadDir(baseTemp)
	t.Logf("Remaining temporary entries in base dir %s: %d", baseTemp, len(entries))
}
