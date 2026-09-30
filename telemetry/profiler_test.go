package telemetry

import (
	"testing"
	"time"

	"axiombox/sandbox"
)

func TestProfilerSampling(t *testing.T) {
	cfg := sandbox.DefaultConfig()
	cfg.TimeLimit = 2 * time.Second

	job, err := sandbox.NewJob(cfg)
	if err != nil {
		t.Fatalf("Failed to create Job Object: %v", err)
	}
	defer job.Close()

	prof := NewProfiler(job, 10*time.Millisecond)

	var sampleCount int
	prof.Start(func(snap MetricSnapshot) {
		sampleCount++
	})

	res, err := job.ExecuteProcess("cmd.exe /c echo TELEMETRY_TEST", "")
	if err != nil {
		t.Fatalf("ExecuteProcess failed: %v", err)
	}

	prof.Stop()

	if res.Status != "COMPLETED" {
		t.Errorf("Expected status COMPLETED, got %s", res.Status)
	}

	snaps := prof.Snapshots()
	t.Logf("Collected %d telemetry snapshots during execution", len(snaps))
}

func TestColdStartBenchmarkQuick(t *testing.T) {
	report, err := RunColdStartBenchmark(5)
	if err != nil {
		t.Fatalf("RunColdStartBenchmark failed: %v", err)
	}

	t.Logf("Benchmark (5 runs): p50=%.2fms, p95=%.2fms, Mean=%.2fms, Speedup=%.1fx",
		report.AxiomBoxP50Ms, report.AxiomBoxP95Ms, report.AxiomBoxMeanMs, report.SpeedupMultiplier)

	if report.AxiomBoxP50Ms <= 0 {
		t.Errorf("Expected positive p50 latency")
	}
}
