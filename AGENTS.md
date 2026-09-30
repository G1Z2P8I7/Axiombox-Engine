# AGENTS.md — AxiomBox

**Project:** AxiomBox — Kernel-Isolated Remote Code Execution (RCE) Sandbox & Resource Governor Engine
**Platform:** Native Windows 11 (Win32 kernel primitives), cross-platform-aware architecture
**Audience:** AI coding agents (and human maintainers) operating on this repository

---

## 1. Purpose & Scope

AxiomBox is a secure, high-throughput, multi-tenant sandbox for executing **untrusted user code** (C++, Python, JavaScript) — the kind of engine that powers platforms like LeetCode, HackerRank, Replit, or Judge0. It replaces slow, heavyweight Docker cold-starts with native **Windows Job Objects** and **restricted security tokens**, giving sub-25ms sandbox spin-up with hard kernel-enforced limits on memory, CPU, process count, and execution time.

This file governs how AI agents working on this repo should divide labor, communicate, validate their work, and hand off tasks. It applies to:
- The kernel isolation layer (`sandbox/`, `security/`)
- The compiler/execution pipeline (`executor/`)
- The orchestration/API layer (backend worker pool, WebSocket telemetry)
- The telemetry/benchmarking layer (`telemetry/`)
- The frontend playground (`frontend/playground/`)

**Out of scope:** this file does not cover general product-management decisions (pricing, hosting, legal). Agents should escalate those to the human maintainer rather than decide unilaterally.

**Safety framing:** AxiomBox's entire purpose is *defensive* isolation of untrusted code (analogous to Docker, gVisor, Judge0). Agents should treat fork-bombs, OOM floods, filesystem traversal, and network-exfiltration attempts strictly as **test cases to contain and validate against** inside the sandbox harness — never as techniques to deploy against the host system, other users, or anything outside the declared benchmark/test suite.

---

## 2. Agent Roles

### 2.1 Planner / Orchestrator Agent
- **Responsibility:** Break incoming feature requests or bug reports into scoped tasks, assign them to the right specialist agent, and sequence work so dependencies (e.g., kernel layer before compiler pipeline) are respected.
- **Capabilities:** Reads `Context.md`/this file, decomposes tasks, opens/tracks issues, requests clarification from the human when specs are ambiguous.
- **Invoked when:** A new feature, bug, or milestone is requested; when multiple agents' outputs need to be reconciled.
- **Never:** Write kernel-level or security-token code itself; merge conflicting agent outputs without a Reviewer pass; silently drop a task instead of flagging a blocker.

### 2.2 Kernel Isolation Agent (Systems)
- **Responsibility:** Owns `sandbox/win_job.go` and `security/token_jail.go` — Windows Job Object configuration and restricted security tokens.
- **Capabilities:** Wraps `kernel32.dll`/`advapi32.dll` via Go `syscall` or Python `ctypes`; configures `JOBOBJECT_EXTENDED_LIMIT_INFORMATION`, `JOBOBJECT_CPU_RATE_CONTROL_INFORMATION`, `JOBOBJECT_BASIC_UI_RESTRICTIONS`; builds restricted tokens via `CreateRestrictedToken`; scopes NTFS ACLs to ephemeral sandbox directories.
- **Invoked when:** Changing resource limits, adding a new containment primitive, hardening privilege stripping, or debugging a sandbox escape/limit-bypass report.
- **Never:** Loosen `JOB_OBJECT_LIMIT_ACTIVE_PROCESSES`, memory caps, or `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE` without an explicit, documented reason and Reviewer sign-off; grant a sandboxed token elevated/admin SIDs; leave a job object or restricted directory without cleanup on any exit path (success, crash, or timeout).

### 2.3 Backend Orchestration Agent
- **Responsibility:** API Gateway, auth/rate-limiting, the async worker dispatch queue, and the ephemeral workspace builder.
- **Capabilities:** Implements FastAPI/Go `net/http` endpoints, WebSocket streaming of stdout/stderr/telemetry, priority worker pools sized to the 14C/20T CPU budget, workspace create/teardown (`C:\Temp\Sandbox_XYZ`).
- **Invoked when:** Adding/modifying an API route, changing queueing or concurrency behavior, wiring a new language into the dispatch flow.
- **Never:** Bypass the Kernel Isolation Agent's containment layer to execute code directly; persist user code/output beyond the documented retention policy; expose internal error traces or host paths to the client.

### 2.4 Compiler Pipeline Agent
- **Responsibility:** Owns `executor/compiler_pipeline.go` — per-language staged compile-then-execute flow for C++ (GCC/MSVC), Python 3, and Node.js.
- **Capabilities:** Compiles with size/time limits, strips or monkey-patches dangerous stdlib access (`os`, `sys` in Python), enforces V8 memory caps and disables network access for Node.
- **Invoked when:** Adding a new language, changing compiler flags, or a language-specific escape/limit-bypass is reported.
- **Never:** Execute untrusted code outside a Job-Object-enforced process; add a language without a matching entry in the Security/Red-Team Agent's adversarial suite; trust compiler exit codes alone as a safety signal.

### 2.5 Frontend / Playground Agent
- **Responsibility:** `frontend/playground/` — the Next.js 14 + Tailwind + Monaco Editor interactive UI, including the "attack preset" buttons (fork bomb, infinite loop, malloc explosion demos).
- **Capabilities:** Editor integration, syntax highlighting, real-time WebSocket telemetry display, layout/responsive/UX work, SEO basics (titles, meta, favicon), error/success state UI.
- **Invoked when:** Building or fixing playground UI, adding a new demo preset, addressing UI/QA checklist items (see §6.2).
- **Never:** Assume any input is safe — the playground is the untrusted-code entry point, so all execution must route through the standard backend/kernel path; hardcode secrets or backend URLs; ship a page that fails the QA checklist in §6.2.

### 2.6 Security / Red-Team Agent
- **Responsibility:** Adversarial validation — fork-bombs, thread-bombs, OOM heap floods, filesystem traversal (`../../Windows/System32`), network exfiltration attempts.
- **Capabilities:** Writes and runs the adversarial test suite against the sandbox (never the bare host), verifies each attack is contained within its documented limit (e.g., fork bomb killed in ~2ms, OOM trips at 64MB).
- **Invoked when:** After any change to `sandbox/`, `security/`, or `executor/`; before any release; when a new attack class is proposed for the suite.
- **Never:** Run adversarial payloads outside the sandboxed/ephemeral environment; test against production data or real user accounts; disable host-level protections (Defender, firewall) to "make a test pass."

### 2.7 Telemetry & Benchmark Agent
- **Responsibility:** `telemetry/profiler.go` — kernel accounting via `QueryInformationJobObject`, cold-start latency benchmarking, concurrency stress testing.
- **Capabilities:** Measures launch-to-execution latency (target: Job Object <25ms vs Docker >1500ms), peak memory (`PeakPagefileUsage`), CPU kernel/user time, 50-concurrent-submission stress runs.
- **Invoked when:** Validating a performance target, investigating a regression, or producing benchmark numbers for a release/report.
- **Never:** Report benchmark numbers without stating hardware/config used; cherry-pick a single best-case run instead of reporting distribution (p50/p95/p99); benchmark against a warm cache and present it as cold-start.

### 2.8 Reviewer / QA Agent
- **Responsibility:** Final gate before anything is considered "done" — code review, security posture check, and the frontend polish checklist.
- **Capabilities:** Re-runs the Security/Red-Team suite and Telemetry benchmarks, walks the QA checklist (§6.2) for any frontend change, checks cleanup paths (no orphaned job objects, sandbox dirs, processes).
- **Invoked when:** Before merging any PR that touches `sandbox/`, `security/`, `executor/`, backend routes, or `frontend/playground/`.
- **Never:** Approve a change that weakens a kernel limit, skips the adversarial suite, or ships a frontend regression from §6.2 without an explicit, logged exception from the human maintainer.

---

## 3. Workflow / Orchestration

1. **Intake:** Planner receives the request, checks it against `Context.md` and this file, and decomposes it into scoped tasks tagged by owning agent (2.1–2.8).
2. **Dependency ordering:** Kernel Isolation → Compiler Pipeline / Backend Orchestration → Frontend, since higher layers assume the containment primitives exist and are trustworthy. Telemetry and Security work run continuously alongside, not strictly after.
3. **Implementation:** Each specialist agent works only within its owned files/modules (§2), following the Communication Protocol (§5) for every unit of work.
4. **Cross-agent handoff:** When one agent's output is a dependency for another (e.g., Kernel Isolation Agent adds a new limit type that Backend Orchestration must expose via API), the producing agent posts a `Result` block (§5) that the consuming agent must read before starting.
5. **Validation:** Security/Red-Team Agent and Telemetry Agent validate any change to the isolation/execution path. Frontend changes go through the §6.2 checklist.
6. **Review gate:** Reviewer/QA Agent is the mandatory final checkpoint (§2.8) before merge/release.
7. **Escalation path:** Any agent hitting an ambiguous spec, a safety-relevant tradeoff (e.g., "should we relax this memory cap for large test suites?"), or a failing quality gate it cannot resolve within its own scope escalates to the Planner, which either resolves it against `Context.md` or surfaces it to the human maintainer. Kernel-limit relaxations and anything touching privilege escalation **always** escalate to the human, never resolved by agents alone.

---

## 4. Tools & Permissions

| Agent | Allowed tools/actions | Restricted |
|---|---|---|
| Kernel Isolation | Win32 API calls via syscall/ctypes, NTFS ACL edits scoped to `C:\Temp\Sandbox_*`, unit tests against job objects | No production host file/network access; no privilege elevation |
| Backend Orchestration | HTTP/WebSocket route edits, queue/worker config, workspace create/delete under the sandbox temp root | Cannot invoke a target process outside a Job Object; no direct filesystem access outside the ephemeral workspace |
| Compiler Pipeline | Invoke compilers/interpreters *only* inside an already-isolated process, stdlib patching for Python | Cannot execute untrusted code pre-isolation "to check syntax" or similar shortcuts |
| Frontend/Playground | Next.js/Tailwind/Monaco edits, calls to the AxiomBox API only (never directly to a compiler/interpreter) | No embedding of backend credentials; no client-side execution of user code |
| Security/Red-Team | Run the adversarial suite against the sandbox harness in a designated test environment | Never target production, the host OS outside the harness, or real user data |
| Telemetry/Benchmark | Read-only `QueryInformationJobObject` calls, benchmark harness execution | Cannot modify limits to inflate benchmark numbers |
| Reviewer/QA | Read access to all modules, re-run of test/benchmark/QA suites | Cannot approve a merge without evidence (logs/benchmark output) attached |

---

## 5. Communication Protocol

Every agent's output — whether to another agent or to the user — should use this structure:

```
### Reasoning
Brief statement of what was needed and why this approach was chosen.

### Plan
Ordered steps about to be taken (skip for trivial one-line fixes).

### Actions
What was actually done — files touched, commands run, tests executed.

### Result
Outcome: pass/fail against the relevant quality gate (§6), benchmark numbers
if applicable, and any residual risk or follow-up needed.

### Open Questions / Escalations
Anything ambiguous, unresolved, or requiring human sign-off. Omit if none.
```

Handoffs between agents must include the **Result** and **Open Questions** sections verbatim so the next agent (or the Reviewer) doesn't have to re-derive context.

---

## 6. Success Criteria & Quality Gates

### 6.1 System-level "done"
- Cold-start latency: Job Object sandbox launch-to-execution < 25ms (vs. Docker baseline > 1500ms), measured and logged.
- Fork-bomb, thread-bomb, and process-count violations are contained without host degradation.
- OOM/heap-flood attempts are capped at the configured per-sandbox limit (32–64MB) and terminated cleanly.
- Filesystem traversal and network exfiltration attempts are blocked and logged.
- 50-concurrent-submission stress test completes with stable queue latency and no leaked job objects, tokens, or temp directories.
- Total memory footprint under heavy multi-tenant load stays ≤ 1.5GB.

### 6.2 Frontend / Playground QA Checklist
Owned by the Frontend Agent, enforced by the Reviewer Agent on every UI change. Currently known failing items are marked ❌ and are the priority fix list; all items must pass before a frontend PR is considered done:

- [ ] No horizontal scroll ❌ *(known issue — priority fix)*
- [ ] No placeholder text left in shipped UI ❌ *(known issue — priority fix)*
- [ ] No unused/dead nav items ❌ *(known issue — priority fix)*
- [ ] All links checked for breakage
- [ ] Mobile menu present and functional
- [ ] Favicon present
- [ ] Page titles set correctly per route
- [ ] Meta descriptions present
- [ ] Footer links correct and working
- [ ] Custom 404 page implemented
- [ ] Copyright year correct/dynamic
- [ ] Images compressed
- [ ] No broken buttons
- [ ] Success messages present for relevant actions (e.g., submission accepted)
- [ ] Error messages present and user-readable (e.g., `TIME_LIMIT_EXCEEDED`, `MEMORY_LIMIT_EXCEEDED`)
- [ ] No mobile overflow issues
- [ ] Logo is clickable (returns to home)
- [ ] Phone/contact number (if shown) is clickable (`tel:` link)

### 6.3 Per-agent quality gates
- **Kernel Isolation:** every new limit has an automated test proving it triggers at the documented threshold.
- **Compiler Pipeline:** every supported language has a passing "happy path" test and a corresponding entry in the Security suite.
- **Backend Orchestration:** every new route has an auth/rate-limit test and a WebSocket telemetry test.
- **Telemetry:** every benchmark claim in a PR description is backed by a logged run, not an estimate.

---

## 7. Error Handling & Recovery

- **Ambiguous spec:** Agent states its interpretation explicitly in `Reasoning`, proceeds with the most conservative (most restrictive/safe) interpretation, and flags it under `Open Questions` for Planner/human confirmation rather than blocking.
- **Kernel API failure (e.g., `CreateJobObjectW`/`SetInformationJobObject` returns an error):** Fail closed — do not execute the untrusted process if containment setup did not fully succeed. Log the Win32 error code and surface a generic `SANDBOX_UNAVAILABLE` to the client, never a raw stack trace.
- **Partial containment (e.g., token restricted but job object limits not yet applied):** Treat as a full failure of the request; never run user code under partial isolation.
- **Test/benchmark failure:** Owning agent attempts one root-cause pass; if unresolved, hands off to Reviewer with full logs rather than retrying blindly or silently lowering the bar (e.g., raising a memory limit to make a test pass).
- **Cleanup failure (orphaned process/job/temp dir):** Treated as a P0 — Kernel Isolation Agent must add a watchdog/second-pass cleanup rather than accepting leaked resources as "rare."
- **Incomplete context from the user/Planner:** Ask one targeted clarifying question rather than guessing on anything safety-relevant (limits, privilege scope, network access).

---

## 8. Style & Tone Conventions

- **Code:** Idiomatic Go or Python matching the existing module (`sandbox/`, `security/`, `executor/`, `telemetry/`). Comment *why* a kernel limit or ACL is set the way it is, not just what the call does — future agents need the rationale to avoid accidentally loosening it.
- **Naming:** Match existing conventions — `win_job.go`, `token_jail.go`, `compiler_pipeline.go`, `profiler.go` — snake_case files, PascalCase exported Go symbols.
- **Commit/PR descriptions:** State the quality gate(s) the change satisfies (§6) and attach benchmark/test evidence, not just a description of the change.
- **Security-sensitive changes** (anything in `sandbox/` or `security/`): explicit, blunt, no hedging — state exactly what limit changed, old value, new value, and why.
- **Frontend copy:** Plain, direct error/success messages (surface the actual limit hit — e.g., "Memory limit (64MB) exceeded" — not a generic "Something went wrong").
- **No scope creep:** an agent should not "helpfully" touch files outside its owned module in §2 without flagging it to the Planner first.
