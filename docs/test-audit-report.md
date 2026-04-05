# Test Audit Report — agent-pool (full scope)

**Date:** 2026-04-05
**Scope:** Full codebase after v0.6 completion
**Go version:** 1.26.1

## Summary

- Source files: 29 | Test files: 25 | Ratio: 1.2:1
- Overall coverage: **69.9%** (threshold: 65%)
- Untested functions: 14 (high risk: 1, medium: 1, low: 0, skip: 12)
- Quality issues: 14 (P0: 0, P1: 2, P2: 4, P3: 2)

## Coverage Gaps (by risk)

### High Risk Untested

| Function | File | Classification | Why It Matters |
|----------|------|---------------|----------------|
| `DiscoverPoolDir` | `internal/config/config.go:89` | Configuration | Used by every CLI command; incorrect discovery could point at wrong pool |

### Medium Risk Untested

| Function | File | Classification | Why It Matters |
|----------|------|---------------|----------------|
| `connectAndSend` | `cmd/agent-pool/main.go:382` | I/O Boundary | Socket client helper; error handling paths (connection refused, timeout) affect UX |

### Not Worth Unit Testing (skipped)

| Function | File | Pattern | Rationale |
|----------|------|---------|-----------|
| `main` | `cmd/agent-pool/main.go:24` | Entry point | Pure dispatch switch, no logic |
| `cmdStart` | `cmd/agent-pool/main.go:56` | Entry point | Orchestration glue (load config, create daemon, block) |
| `cmdStop` | `cmd/agent-pool/main.go:118` | Thin wrapper | Calls `connectAndSend("stop")` + prints result |
| `cmdStatus` | `cmd/agent-pool/main.go:144` | CLI rendering | JSON pretty-print; no logic worth testing |
| `cmdWatch` | `cmd/agent-pool/main.go:224` | CLI rendering | ANSI formatting; no logic worth testing |
| `cmdMCP` | `cmd/agent-pool/main.go:418` | Entry point | Flag parsing + `agentmcp.Run()` delegation |
| `cmdFlush` | `cmd/agent-pool/main.go:465` | Thin wrapper | Flag parsing + `hooks.Flush()` delegation |
| `cmdGuard` | `cmd/agent-pool/main.go:489` | Thin wrapper | Flag parsing + `hooks.Guard()` delegation |
| `newStderrLogger` | `cmd/agent-pool/main.go:515` | Trivial factory | Single expression, zero branching |
| `parseFlags` | `cmd/agent-pool/main.go:522` | Thin wrapper | Delegates to `parseFlagsFromArgs` (100% covered) |
| `printUsage` | `cmd/agent-pool/main.go:543` | Help text | Framework glue, no logic |
| `defaultSpawner.Spawn` | `internal/daemon/daemon.go:37` | Thin wrapper | Single delegation to `expert.Spawn` |

### Partially Covered (worth noting)

| Function | File | Coverage | Gap |
|----------|------|----------|-----|
| `Status` | `daemon.go:1031` | 62.5% | Active tasks branch untested |
| `ParseHumanInbox` | `presenter.go:116` | 60.0% | Telegram/file modes untested (future) |
| `WriteFile` | `atomicfile.go:13` | 45.5% | Fsync/rename error paths |
| `resolveProjectDir` | `daemon.go:1103` | 60.0% | Tilde expansion path |

## Quality Issues

### P0 — Likely Catching Zero Bugs

None found.

### P1 — Masking Real Issues

**1. `time.Sleep` for synchronization (30+ occurrences)**
- Files: `daemon_test.go`, `watcher_test.go`, `approval_test.go`, `socket_test.go`
- Pattern: `time.Sleep(50ms)` to `time.Sleep(2s)` for goroutine synchronization
- Risk: Flaky on slow CI runners or under load
- Mitigation: `waitForTaskStatus` polling helper exists but many tests use raw sleeps

**2. `time.Now()` in tests without injection**
- Files: `taskboard_test.go:109`, `contract_test.go:173`
- Pattern: Captures before/after timestamps for boundary checks
- Risk: Nanosecond-boundary failures (extremely rare but non-deterministic)

### P2 — Test Debt

**3. Polling loops instead of event-driven sync**
- File: `daemon_test.go` (6+ locations)
- Pattern: `deadline := time.Now().Add(5*time.Second)` with sleep loop
- Better: Channel-based signaling or sync.WaitGroup

**4. Existence-only assertions**
- Files: `daemon_test.go`, `router_test.go`
- Pattern: `os.Stat(path)` checks file exists but not content

**5. `connectSocket` retry loop**
- File: `socket_test.go:47-63`
- Pattern: 20 retries with 50ms sleep to wait for socket readiness

**6. Test helper complexity**
- File: `daemon_test.go`
- `startTestDaemon` does socket path override, background goroutine, 500ms sleep

### P3 — Notes

**7. Repeated pool config setup**
- File: `daemon_test.go` — 15+ tests write nearly identical pool.toml configs

**8. `WithSocketPath` at 0% coverage**
- Used conditionally by `startTestDaemon` path-length check; tool doesn't see it

## Recommended Next Steps

1. **Write tests for `DiscoverPoolDir`** — high-risk config function with directory traversal logic
2. **Write tests for `connectAndSend`** — medium-risk socket client with error handling paths
3. **Replace readiness sleep** — the 500ms sleep in `startTestDaemon` is the root of downstream flakiness
4. **No immediate action on P2/P3** — debt indicators, not bugs; address when touching those files
