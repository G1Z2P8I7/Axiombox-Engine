// Package server provides the HTTP/WebSocket API Gateway, rate limiting, and real-time telemetry streaming for AxiomBox.
package server

import (
	"encoding/json"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"axiombox/executor"
	"axiombox/orchestrator"
	"axiombox/telemetry"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow development and playground origins
	},
}

// RateLimiter implements a simple sliding window per IP.
type RateLimiter struct {
	mu      sync.Mutex
	clients map[string][]time.Time
	limit   int
	window  time.Duration
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		clients: make(map[string][]time.Time),
		limit:   limit,
		window:  window,
	}
	// Cleanup routine every minute
	go func() {
		ticker := time.NewTicker(time.Minute)
		for range ticker.C {
			rl.mu.Lock()
			now := time.Now()
			for ip, times := range rl.clients {
				var valid []time.Time
				for _, t := range times {
					if now.Sub(t) < rl.window {
						valid = append(valid, t)
					}
				}
				if len(valid) == 0 {
					delete(rl.clients, ip)
				} else {
					rl.clients[ip] = valid
				}
			}
			rl.mu.Unlock()
		}
	}()
	return rl
}

func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	times := rl.clients[ip]
	var valid []time.Time
	for _, t := range times {
		if now.Sub(t) < rl.window {
			valid = append(valid, t)
		}
	}

	if len(valid) >= rl.limit {
		rl.clients[ip] = valid
		return false
	}

	rl.clients[ip] = append(valid, now)
	return true
}

// Server encapsulates the HTTP handler and worker pool.
type Server struct {
	pool        *orchestrator.WorkerPool
	rateLimiter *RateLimiter
	mux         *http.ServeMux
}

// NewServer initializes the API Gateway and routes.
func NewServer(concurrency int, queueCap int) *Server {
	s := &Server{
		pool:        orchestrator.NewWorkerPool(concurrency, queueCap),
		rateLimiter: NewRateLimiter(60, time.Minute), // 60 requests per minute
		mux:         http.NewServeMux(),
	}

	s.routes()
	return s
}

// Handler returns the HTTP handler with CORS middleware.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		s.mux.ServeHTTP(w, r)
	})
}

func (s *Server) routes() {
	s.mux.HandleFunc("/api/health", s.handleHealth)
	s.mux.HandleFunc("/api/execute", s.handleExecute)
	s.mux.HandleFunc("/api/ws/execute", s.handleWebSocketExecute)
	s.mux.HandleFunc("/api/benchmark", s.handleBenchmark)
	s.mux.HandleFunc("/api/chaos/presets", s.handleChaosPresets)
}

func (s *Server) getClientIP(r *http.Request) string {
	xfwd := r.Header.Get("X-Forwarded-For")
	if xfwd != "" {
		parts := strings.Split(xfwd, ",")
		return strings.TrimSpace(parts[0])
	}
	parts := strings.Split(r.RemoteAddr, ":")
	if len(parts) > 0 {
		return parts[0]
	}
	return r.RemoteAddr
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	active, queueDepth, totalServed := s.pool.Stats()

	resp := map[string]interface{}{
		"status":          "HEALTHY",
		"version":         "1.0.0",
		"arch":            runtime.GOARCH,
		"os":              runtime.GOOS,
		"cpu_cores":       runtime.NumCPU(),
		"active_sandboxes": active,
		"queue_depth":     queueDepth,
		"total_served":    totalServed,
		"timestamp":       time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ip := s.getClientIP(r)
	if !s.rateLimiter.Allow(ip) {
		http.Error(w, `{"error":"RATE_LIMIT_EXCEEDED","message":"Too many requests, please slow down."}`, http.StatusTooManyRequests)
		return
	}

	var req executor.PipelineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"INVALID_JSON","message":"Failed to parse request body."}`, http.StatusBadRequest)
		return
	}

	resCh, err := s.pool.Submit(req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":   "QUEUE_FULL",
			"message": err.Error(),
		})
		return
	}

	taskRes := <-resCh
	if taskRes.Err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":   "EXECUTION_FAILED",
			"message": "Internal sandbox error occurred",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(taskRes.Result)
}

type wsMessage struct {
	Type string      `json:"type"` // "stdout", "stderr", "telemetry", "finished", "error"
	Data interface{} `json:"data"`
}

func (s *Server) handleWebSocketExecute(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	ip := s.getClientIP(r)
	if !s.rateLimiter.Allow(ip) {
		_ = conn.WriteJSON(wsMessage{Type: "error", Data: "Rate limit exceeded"})
		return
	}

	_, msgBytes, err := conn.ReadMessage()
	if err != nil {
		return
	}

	var req executor.PipelineRequest
	if err := json.Unmarshal(msgBytes, &req); err != nil {
		_ = conn.WriteJSON(wsMessage{Type: "error", Data: "Invalid JSON format"})
		return
	}

	// Submit to worker pool
	resCh, err := s.pool.Submit(req)
	if err != nil {
		_ = conn.WriteJSON(wsMessage{Type: "error", Data: err.Error()})
		return
	}

	taskRes := <-resCh
	if taskRes.Err != nil {
		_ = conn.WriteJSON(wsMessage{Type: "error", Data: "Sandbox execution failed"})
		return
	}

	res := taskRes.Result
	if len(res.Stdout) > 0 {
		_ = conn.WriteJSON(wsMessage{Type: "stdout", Data: res.Stdout})
	}
	if len(res.Stderr) > 0 {
		_ = conn.WriteJSON(wsMessage{Type: "stderr", Data: res.Stderr})
	}

	_ = conn.WriteJSON(wsMessage{
		Type: "telemetry",
		Data: map[string]interface{}{
			"peak_memory_mb":        res.PeakMemoryMB,
			"execution_time_ms":     res.ExecutionTimeMs,
			"cpu_kernel_time_us":    res.CpuKernelTimeUs,
			"cpu_user_time_us":      res.CpuUserTimeUs,
			"cold_start_latency_ms": res.ColdStartLatencyMs,
		},
	})

	_ = conn.WriteJSON(wsMessage{
		Type: "finished",
		Data: res,
	})
}

func (s *Server) handleBenchmark(w http.ResponseWriter, r *http.Request) {
	report, err := telemetry.RunColdStartBenchmark(10)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

func (s *Server) handleChaosPresets(w http.ResponseWriter, r *http.Request) {
	presets := map[string]interface{}{
		"fork_bomb_c": map[string]string{
			"title":       "Fork Bomb (Process Flood)",
			"language":    "cpp",
			"description": "Attempts recursive process spawning; contained at 1 process by kernel limits in <25ms.",
			"code": `#include <windows.h>
#include <iostream>

int main() {
    std::cout << "[Attacker] Attempting fork bomb process spawning...\n";
    STARTUPINFOA si = { sizeof(si) };
    PROCESS_INFORMATION pi;
    for (int i = 0; i < 50; i++) {
        // Attempt spawning child processes recursively
        CreateProcessA(NULL, (LPSTR)"cmd.exe /c echo Child", NULL, NULL, FALSE, 0, NULL, NULL, &si, &pi);
    }
    std::cout << "[Attacker] Spawn attempts finished.\n";
    return 0;
}`,
		},
		"infinite_loop": map[string]string{
			"title":       "Infinite Loop (CPU Starvation)",
			"language":    "python",
			"description": "Consumes 100% CPU in an infinite loop; killed at exactly 2.0s deadline by the watchdog.",
			"code": `import time

print("[Attacker] Starting infinite CPU burn loop...")
counter = 0
while True:
    counter += 1
`,
		},
		"malloc_explosion": map[string]string{
			"title":       "10GB Malloc Explosion (OOM Flood)",
			"language":    "cpp",
			"description": "Attempts allocating 10GB of RAM; instantly intercepted at 64MB kernel limit without OS freeze.",
			"code": `#include <iostream>
#include <vector>

int main() {
    std::cout << "[Attacker] Attempting 10GB heap allocation...\n";
    size_t chunkSize = 100 * 1024 * 1024; // 100MB chunks
    std::vector<char*> chunks;
    for (int i = 0; i < 100; i++) {
        char* mem = new char[chunkSize];
        // Dirty the pages to trigger commit
        for (size_t j = 0; j < chunkSize; j += 4096) {
            mem[j] = 1;
        }
        chunks.push_back(mem);
        std::cout << "Allocated " << (i + 1) * 100 << " MB\n";
    }
    return 0;
}`,
		},
		"filesystem_traversal": map[string]string{
			"title":       "Directory Traversal Attempt",
			"language":    "python",
			"description": "Attempts reading Windows System32 host files; blocked by token jail and ephemeral sandbox scoping.",
			"code": `import os

print("[Attacker] Attempting to read Windows System32 hosts file...")
target = r"C:\Windows\System32\drivers\etc\hosts"
try:
    with open(target, "r") as f:
        print("[BREACH] File content:\n" + f.read())
except Exception as e:
    print(f"[CONTAINED] Access blocked: {e}")
`,
		},
		"cpp_quicksort": map[string]string{
			"title":       "C++ Fast Sort & Telemetry",
			"language":    "cpp",
			"description": "High-speed randomized quicksort demonstrating microsecond CPU profiling.",
			"code": `#include <iostream>
#include <vector>
#include <algorithm>
#include <random>

int main() {
    std::vector<int> nums(50000);
    std::mt19937 rng(42);
    for (auto &x : nums) x = rng() % 1000000;
    
    std::sort(nums.begin(), nums.end());
    std::cout << "Successfully sorted " << nums.size() << " elements!\n";
    std::cout << "First: " << nums.front() << ", Last: " << nums.back() << "\n";
    return 0;
}`,
		},
		"python_matrix": map[string]string{
			"title":       "Python Fibonacci Generator",
			"language":    "python",
			"description": "Valid Python computation with microsecond kernel accounting.",
			"code": `def fibonacci(n):
    a, b = 0, 1
    for _ in range(n):
        yield a
        a, b = b, a + b

nums = list(fibonacci(25))
print(f"Computed 25 Fibonacci numbers:")
print(nums)
`,
		},
		"js_prime_sieve": map[string]string{
			"title":       "JavaScript Prime Sieve",
			"language":    "javascript",
			"description": "V8 memory-capped execution of Sieve of Eratosthenes.",
			"code": `function sieve(max) {
    const isPrime = new Uint8Array(max + 1).fill(1);
    isPrime[0] = isPrime[1] = 0;
    for (let i = 2; i * i <= max; i++) {
        if (isPrime[i]) {
            for (let j = i * i; j <= max; j += i) isPrime[j] = 0;
        }
    }
    let primes = [];
    for (let i = 2; i <= max; i++) if (isPrime[i]) primes.push(i);
    return primes;
}

const primes = sieve(10000);
console.log("Found " + primes.length + " primes up to 10000.");
console.log("Largest prime found: " + primes[primes.length - 1]);
`,
		},
		"java_quicksort": map[string]string{
			"title":       "Java Matrix Multiplier",
			"language":    "java",
			"description": "HotSpot JVM sandboxed execution with single-threaded GC and RAM capping.",
			"code": `public class Main {
    public static void main(String[] args) {
        int n = 100;
        int[][] a = new int[n][n];
        int[][] b = new int[n][n];
        int[][] c = new int[n][n];

        for (int i = 0; i < n; i++) {
            for (int j = 0; j < n; j++) {
                a[i][j] = i + j;
                b[i][j] = i * 2;
            }
        }

        for (int i = 0; i < n; i++) {
            for (int j = 0; j < n; j++) {
                for (int k = 0; k < n; k++) {
                    c[i][j] += a[i][k] * b[k][j];
                }
            }
        }

        System.out.println("Java Matrix Multiplication completed (100x100)");
        System.out.println("Result at [50][50]: " + c[50][50]);
    }
}`,
		},
		"csharp_linq": map[string]string{
			"title":       "C# Parallel LINQ & Sort",
			"language":    "csharp",
			"description": "Microsoft Visual C# sandboxed execution with native binary compilation.",
			"code": `using System;
using System.Collections.Generic;
using System.Linq;

class Program {
    static void Main() {
        var rng = new Random(42);
        var numbers = Enumerable.Range(0, 50000)
            .Select(_ => rng.Next(1, 1000000))
            .ToList();

        var evensSorted = numbers
            .Where(x => x % 2 == 0)
            .OrderBy(x => x)
            .Take(10)
            .ToList();

        Console.WriteLine("C# Process Executed Inside Win32 Job Object!");
        Console.WriteLine("Top 10 sorted even numbers: " + string.Join(", ", evensSorted));
    }
}`,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(presets)
}
