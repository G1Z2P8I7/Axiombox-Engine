# Project 4: AxiomBox — Kernel-Isolated Remote Code Execution (RCE) Sandbox & Resource Governor Engine

## 1. Project Overview & Objective
A secure, high-throughput, multi-tenant untrusted code execution sandbox and resource governor engineered natively for **Windows 11** (leveraging native Win32 Kernel primitives) with cross-platform architecture patterns.

Building platforms like **LeetCode, HackerRank, Replit, or Judge0** requires running arbitrary, untrusted user code (Python, C++, JavaScript) safely in production. Naive implementations using `subprocess.run()` or standard Docker containers fall victim to:
1. **Severe Cold-Start Latency**: Spinning up a fresh Docker container takes 500ms to 2000ms, making interactive code execution feel sluggish.
2. **Denial-of-Service (DoS) Attacks**: Malicious users can execute **fork-bombs** (`while(1) fork()`), spawning thousands of processes that lock up the entire operating system kernel.
3. **Memory & CPU Starvation**: Infinite loops or recursive memory allocations (`malloc`) consume all host RAM, triggering OS crashes.
4. **Filesystem & Network Breaches**: Untrusted scripts can read environment variables, access local databases, or scan the private host network.

### AxiomBox Novelty & Systems Innovations:
* **Sub-25ms Native Process Sandboxing (Windows Job Objects)**: Replaces slow container cold starts by harnessing **Windows Kernel Job Objects** (`CreateJobObjectW`, `SetInformationJobObject`). Enforces sub-millisecond process containment with hard caps on memory, CPU rate cycles, active process count, and execution deadlines.
* **Fork-Bomb & Process Tree Containment**: Configures `JOB_OBJECT_LIMIT_ACTIVE_PROCESSES = 1` and `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`. The moment an untrusted binary attempts to spawn unauthorized child processes or times out, the entire process hierarchy is terminated instantly by the Windows kernel.
* **Low-Privilege Restrictive Security Tokens**: Generates restricted security access tokens (`CreateRestrictedToken`) stripping administrative SIDs, disabling privileges, and restricting file I/O strictly to an ephemeral, isolated temporary working directory.
* **High-Throughput Asynchronous Worker Pool**: Centralized job orchestrator in **Go / Python** utilizing priority worker pools to ingest, compile, isolate, execute, and stream telemetry (`stdout`, `stderr`, execution time, peak memory) in real time over WebSockets.

---

## 2. Hardware Resource Budget (ASUS TUF F15 - Windows 11)
* **Execution Environment**: Native Windows 64-bit Kernel API (`kernel32.dll`, `advapi32.dll`).
* **CPU (Intel Core i7-12700H - 14 Cores / 20 Threads)**:
  * Leverages thread concurrency to manage simultaneous execution pools (up to 30 concurrent sandbox runs without host performance degradation).
* **RAM (16 GB DDR5)**:
  * Strict memory bounding: Each sandbox is hard-capped at **32 MB to 64 MB RAM**.
  * Total memory footprint under heavy multi-tenant load remains $\le$ **1.5 GB RAM**.
* **GPU**:
  * Zero GPU usage required (keeps your RTX 4060 100% idle and available for other tasks).

---

## 3. End-to-End System Architecture

```
                  [ Web Client / Code Editor UI ]
                                │
                                ▼ JSON Payload (Code, Language, Stdin)
                  ┌───────────────────────────────┐
                  │    AxiomBox API Gateway       │
                  │  (Rate Limiting & Auth Token) │
                  └──────────────┬────────────────┘
                                │
                                ▼ Worker Dispatch Queue
                  ┌───────────────────────────────┐
                  │   Ephemeral Workspace Builder │
                  │  (Creates C:\Temp\Sandbox_XYZ)│
                  └──────────────┬────────────────┘
                                │
                                ▼
         ┌─────────────────────────────────────────────────┐
         │          Win32 Kernel Isolation Barrier         │
         │                                                 │
         │  1. Restricted Security Token (Strip Privileges)│
         │  2. Windows Job Object Enforcements:            │
         │     • JOB_OBJECT_LIMIT_PROCESS_MEMORY (64 MB)   │
         │     • JOB_OBJECT_LIMIT_ACTIVE_PROCESSES (1)     │
         │     • JOB_OBJECT_CPU_RATE_CONTROL (1 Core Max)  │
         │     • Real-Time Watchdog Timer (2.0s Deadline)  │
         └──────────────────────┬──────────────────────────┘
                                │
                                ▼ Executes Compiler / Interpreter
                  ┌───────────────────────────────┐
                  │ Target Process Execution      │
                  │ (C++ gcc / Python 3 / Node.js)│
                  └──────────────┬────────────────┘
                                │
            ┌───────────────────┴───────────────────┐
            ▼                                       ▼
    [ Normal Exit: 0 ]                     [ Security / Limit Breach ]
  Captures stdout & stats                 Terminates Process Tree (SIGKILL)
            │                                       │
            └───────────────────┬───────────────────┘
                                │
                                ▼
                  ┌───────────────────────────────┐
                  │ Telemetry & Workspace Cleanup │
                  │ (Deletes C:\Temp\Sandbox_XYZ) │
                  └──────────────┬────────────────┘
                                │
                                ▼ WebSocket / JSON Response
                  [ Interactive Code Playground ]
```

### Core Architectural Modules:

#### 1. `sandbox/win_job.go` (Kernel Job Object Manager)
* Wraps Windows C-APIs (`kernel32.dll`) using Go `syscall` / Python `ctypes`.
* Configures:
  - `JOBOBJECT_EXTENDED_LIMIT_INFORMATION`: Restricts maximum committed memory (`ProcessMemoryLimit` = 64MB).
  - `JOBOBJECT_CPU_RATE_CONTROL_INFORMATION`: Hard caps CPU utilization percentage.
  - `JOBOBJECT_BASIC_UI_RESTRICTIONS`: Blocks access to the Windows desktop, global atom tables, and clipboard reading/writing.

#### 2. `security/token_jail.go` (Restricted Security Tokens)
* Creates a sandboxed execution context using `CreateRestrictedToken`.
* Disables all high-level permissions (`SeDebugPrivilege`, `SeShutdownPrivilege`, network socket access).
* Creates ephemeral execution directories with strictly scoped NTFS Access Control Lists (ACLs).

#### 3. `executor/compiler_pipeline.go` (Multi-Language Pipeline)
* Automated multi-stage pipeline:
  * **C++ (GCC/MSVC)**: Staged compilation with size/time limits $\to$ Sandboxed binary execution.
  * **Python 3**: Sandboxed interpreter execution with stripped standard library imports (`os`, `sys` monkey-patching and security flags).
  * **JavaScript (Node.js)**: V8 memory-capped execution with disabled network access.

#### 4. `telemetry/profiler.go` (Resource Telemetry Collector)
* Queries kernel accounting via `QueryInformationJobObject`.
* Extracts exact microsecond CPU kernel/user execution time and peak memory consumption (`PeakPagefileUsage`).

#### 5. `frontend/playground/` (LeetCode-Style Interactive Web UI)
* Sleek Monaco Editor (VS Code web editor) supporting syntax highlighting for C++, Python, and JavaScript.
* **Interactive Attack Presets & Chaos Buttons**:
  - *"Try Fork Bomb"* $\to$ Watch the sandbox kill the child process in 2ms without OS freeze.
  - *"Try Infinite Loop"* $\to$ Watch the hardware watchdog trigger `TIME_LIMIT_EXCEEDED` at exactly 2.000s.
  - *"Try Memory Allocation Explosion (10GB Malloc)"* $\to$ Watch the kernel trigger `MEMORY_LIMIT_EXCEEDED` at 64MB.

---

## 4. Benchmark & Security Validation Suite
1. **Cold-Start Launch Latency**:
   * Evaluates launch-to-execution latency: Win32 Job Object ($<25\text{ ms}$) vs. Docker Windows Container ($>1500\text{ ms}$).
2. **Adversarial Security Hardening Tests**:
   * Validates resilience against:
     - Fork-bombs / Thread-bombs (Max process violations).
     - Out-Of-Memory (OOM) heap flooding attacks.
     - Filesystem traversal attempts (`../../Windows/System32`).
     - Network exfiltration attempts (Socket connection blocks).
3. **Concurrency Stress Test**:
   * Simulates 50 concurrent submission requests benchmarking queue latency and peak system stability.

---

## 5. Technology Stack
* **Core Systems Language**: Go (Golang 1.22+) or Python with `ctypes` / Win32 API extensions
* **Windows Kernel APIs**: `kernel32.dll` (Job Objects, Process Trees), `advapi32.dll` (Security Tokens & ACLs)
* **Compilers & Runtimes**: MinGW-w64 (GCC for C++), Native Python 3.11, Node.js
* **Backend API**: FastAPI / Go `net/http`, WebSockets
* **Interactive Frontend**: Next.js 14, Tailwind CSS, Monaco Editor (VS Code web editor)
