// Package telemetry provides real-time Win32 kernel profiling, microsecond accounting, and cold-start benchmarking.
package telemetry

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"axiombox/sandbox"
)

// MetricSnapshot captures instantaneous resource consumption from the Windows kernel.
type MetricSnapshot struct {
	Timestamp            time.Time `json:"timestamp"`
	CpuUserMicroseconds  int64     `json:"cpu_user_us"`
	CpuKernelMicroseconds int64    `json:"cpu_kernel_us"`
	TotalCpuMicroseconds int64     `json:"total_cpu_us"`
	PeakMemoryBytes      uint64    `json:"peak_memory_bytes"`
	PeakMemoryMB         float64   `json:"peak_memory_mb"`
	TotalProcesses       uint32    `json:"total_processes"`
	ActiveProcesses      uint32    `json:"active_processes"`
	PageFaultCount       uint32    `json:"page_fault_count"`
	ReadTransferBytes    uint64    `json:"read_transfer_bytes"`
	WriteTransferBytes   uint64    `json:"write_transfer_bytes"`
}

// Profiler continuously polls and records kernel accounting metrics for an active Job Object.
type Profiler struct {
	job        *sandbox.Job
	snapshots  []MetricSnapshot
	mu         sync.RWMutex
	stopChan   chan struct{}
	interval   time.Duration
	isSampling bool
}

// NewProfiler constructs a new resource telemetry profiler.
func NewProfiler(job *sandbox.Job, sampleInterval time.Duration) *Profiler {
	if sampleInterval <= 0 {
		sampleInterval = 25 * time.Millisecond
	}
	return &Profiler{
		job:       job,
		snapshots: make([]MetricSnapshot, 0, 64),
		stopChan:  make(chan struct{}),
		interval:  sampleInterval,
	}
}

// Start initiates periodic background kernel polling.
func (p *Profiler) Start(onSnapshot func(MetricSnapshot)) {
	p.mu.Lock()
	if p.isSampling {
		p.mu.Unlock()
		return
	}
	p.isSampling = true
	p.mu.Unlock()

	ticker := time.NewTicker(p.interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-p.stopChan:
				return
			case t := <-ticker.C:
				snap, err := p.Sample(t)
				if err == nil {
					p.mu.Lock()
					p.snapshots = append(p.snapshots, snap)
					p.mu.Unlock()

					if onSnapshot != nil {
						onSnapshot(snap)
					}
				}
			}
		}
	}()
}

// Sample immediately queries kernel32.dll for the job's instantaneous accounting stats.
func (p *Profiler) Sample(t time.Time) (MetricSnapshot, error) {
	acct, err := p.job.QueryAccounting()
	if err != nil {
		return MetricSnapshot{}, err
	}
	ext, err := p.job.QueryExtendedInfo()
	if err != nil {
		return MetricSnapshot{}, err
	}

	peakBytes := uint64(ext.PeakJobMemoryUsed)
	if peakBytes == 0 {
		peakBytes = uint64(ext.PeakProcessMemoryUsed)
	}

	cpuUserUs := acct.TotalUserTime / 10
	cpuKernelUs := acct.TotalKernelTime / 10

	return MetricSnapshot{
		Timestamp:            t,
		CpuUserMicroseconds:  cpuUserUs,
		CpuKernelMicroseconds: cpuKernelUs,
		TotalCpuMicroseconds: cpuUserUs + cpuKernelUs,
		PeakMemoryBytes:      peakBytes,
		PeakMemoryMB:         float64(peakBytes) / (1024 * 1024),
		TotalProcesses:       acct.TotalProcesses,
		ActiveProcesses:      acct.ActiveProcesses,
		PageFaultCount:       acct.TotalPageFaultCount,
		ReadTransferBytes:    ext.IoInfo.ReadTransferCount,
		WriteTransferBytes:   ext.IoInfo.WriteTransferCount,
	}, nil
}

// Stop terminates background sampling.
func (p *Profiler) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.isSampling {
		close(p.stopChan)
		p.isSampling = false
	}
}

// Snapshots returns a copy of all collected telemetry points.
func (p *Profiler) Snapshots() []MetricSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	copied := make([]MetricSnapshot, len(p.snapshots))
	copy(copied, p.snapshots)
	return copied
}

// BenchmarkResult holds empirical latency distributions for cold start evaluation.
type BenchmarkResult struct {
	TotalRuns         int     `json:"total_runs"`
	AxiomBoxP50Ms     float64 `json:"axiombox_p50_ms"`
	AxiomBoxP95Ms     float64 `json:"axiombox_p95_ms"`
	AxiomBoxP99Ms     float64 `json:"axiombox_p99_ms"`
	AxiomBoxMeanMs    float64 `json:"axiombox_mean_ms"`
	DockerBaselineMs  float64 `json:"docker_baseline_ms"`
	SpeedupMultiplier float64 `json:"speedup_multiplier"`
	PassedTarget      bool    `json:"passed_target"` // True if p50 < 25ms
}

// RunColdStartBenchmark executes multiple fast sandbox launches to compute p50/p95/p99 latency.
func RunColdStartBenchmark(runs int) (*BenchmarkResult, error) {
	if runs <= 0 {
		runs = 30
	}

	latencies := make([]float64, 0, runs)
	var sum float64

	for i := 0; i < runs; i++ {
		cfg := sandbox.DefaultConfig()
		cfg.TimeLimit = 2 * time.Second

		start := time.Now()
		job, err := sandbox.NewJob(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed creating job on run %d: %w", i, err)
		}

		res, err := job.ExecuteProcess("cmd.exe /c echo benchmark", "")
		_ = job.Close()
		if err != nil {
			return nil, fmt.Errorf("failed executing run %d: %w", i, err)
		}

		// Use measured launch-to-execution latency
		latMs := float64(res.ColdStartLatency.Microseconds()) / 1000.0
		if latMs == 0 {
			latMs = float64(time.Since(start).Microseconds()) / 1000.0
		}

		latencies = append(latencies, latMs)
		sum += latMs
	}

	sort.Float64s(latencies)

	p50 := percentile(latencies, 50)
	p95 := percentile(latencies, 95)
	p99 := percentile(latencies, 99)
	mean := sum / float64(runs)

	dockerBaseline := 1500.0 // Standard Docker Windows cold-start latency baseline: 1500ms
	speedup := dockerBaseline / math.Max(p50, 1.0)

	return &BenchmarkResult{
		TotalRuns:         runs,
		AxiomBoxP50Ms:     math.Round(p50*100) / 100,
		AxiomBoxP95Ms:     math.Round(p95*100) / 100,
		AxiomBoxP99Ms:     math.Round(p99*100) / 100,
		AxiomBoxMeanMs:    math.Round(mean*100) / 100,
		DockerBaselineMs:  dockerBaseline,
		SpeedupMultiplier: math.Round(speedup*10) / 10,
		PassedTarget:      p50 < 25.0,
	}, nil
}

func percentile(sorted []float64, p int) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := float64(p) / 100.0 * float64(len(sorted)-1)
	lower := int(index)
	upper := lower + 1
	if upper >= len(sorted) {
		return sorted[lower]
	}
	weight := index - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}
