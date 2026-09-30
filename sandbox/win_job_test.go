package sandbox

import (
	"strings"
	"testing"
	"time"
)

func TestJobObjectBasicExecution(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TimeLimit = 3 * time.Second

	job, err := NewJob(cfg)
	if err != nil {
		t.Fatalf("Failed to create Job Object: %v", err)
	}
	defer job.Close()

	res, err := job.ExecuteProcess("cmd.exe /c echo HELLO_AXIOMBOX", "")
	if err != nil {
		t.Fatalf("ExecuteProcess failed: %v", err)
	}

	if !strings.Contains(res.Stdout, "HELLO_AXIOMBOX") {
		t.Errorf("Expected stdout to contain 'HELLO_AXIOMBOX', got: %s", res.Stdout)
	}

	if res.Status != "COMPLETED" {
		t.Errorf("Expected status COMPLETED, got: %s", res.Status)
	}

	t.Logf("Cold start latency: %v", res.ColdStartLatency)
	t.Logf("Execution time: %v", res.ExecutionTime)
	t.Logf("CPU Kernel Time: %d us, User Time: %d us", res.CpuKernelTimeUs, res.CpuUserTimeUs)
	t.Logf("Peak memory: %.2f MB", res.PeakMemoryMB)
}

func TestJobObjectTimeoutEnforcement(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TimeLimit = 500 * time.Millisecond // Short 500ms timeout

	job, err := NewJob(cfg)
	if err != nil {
		t.Fatalf("Failed to create Job Object: %v", err)
	}
	defer job.Close()

	// Run ping directly (1 process) which takes > 2 seconds
	res, err := job.ExecuteProcess("ping.exe 127.0.0.1 -n 5", "")
	if err != nil {
		t.Fatalf("ExecuteProcess failed: %v", err)
	}

	if res.Status != "TIME_LIMIT_EXCEEDED" {
		t.Errorf("Expected status TIME_LIMIT_EXCEEDED, got: %s", res.Status)
	}

	t.Logf("Timeout triggered at: %v (configured limit: 500ms)", res.ExecutionTime)
}
