# Test Audit Report -- agent-pool v0.3 (branch scope)

**Branch:** `feat/v0.3-taskboard-dependencies` vs `main`
**Date:** 2026-04-03
**Scope:** Files changed on branch only

## Summary

- **Source files changed:** 8 | **Test files changed:** 5 | **Ratio:** 1.6:1
- **Overall coverage (audited packages):** 77.0%
- **Packages audited:** `taskboard`, `daemon`, `config`, `expert`, `mail`

| Package | Coverage | Notes |
|---------|----------|-------|
| `internal/taskboard` | 87.7% | New package (v0.3). DAG + board well-tested; persistence gaps |
| `internal/mail` | 89.2% | Strong parser coverage; `copyFile` under-tested |
| `internal/config` | 84.8% | One branch in `LoadPool` uncovered (~tilde edge) |
| `internal/daemon` | 77.1% | Integration-style tests; several functions below 70% |
| `internal/expert` | 63.0% | `Spawn` at 0% (I/O boundary); state helpers mixed |

---

## Coverage Gaps by Risk

### High Risk

#### `expert.Spawn` -- 0.0%
- **Classification:** I/O Boundary (exec.Command, process signals, env vars)
- **Why high:** This is the core spawning function. It handles process lifecycle, SIGTERM/SIGKILL grace periods, timeout contexts, exit code extraction, and env var injection. A bug here causes silent task failures or zombie processes.
- **What's missing:** No direct test. The daemon tests exercise it indirectly through `fakeSpawner`, but the real `Spawn` is never tested. Error paths (claude not in PATH, process signal handling, context timeout mid-spawn) are completely uncovered.
- **Recommendation:** Extract a `run()` function that takes an `exec.Cmd` (or build the cmd in a testable factory). Test signal handling, exit code extraction, and env var injection with a small Go binary as a stand-in.

#### `daemon.handleCancel` -- 66.7%
- **Classification:** State Machine (status checks, branching on task state)
- **Why high:** Cancel messages affect task lifecycle. The uncovered branch is the `StatusActive` cancel-note path when no `CancelNote` was already set. Edge case: cancelling a task that was already cancelled (double-cancel).
- **What's missing:** Cancel for already-completed/failed tasks (the terminal no-op branch gets log coverage but the board interaction is uncovered). Cancel with missing `cancels` field.
- **Recommendation:** Add test for cancel targeting an already-completed task. Add test for cancel message with empty `cancels` field.

#### `daemon.registerTask` -- 64.3%
- **Classification:** State Machine (ValidateAdd + Add + Save)
- **Why high:** If ValidateAdd rejects a cycle-creating task, the error path logs but never saves -- this path is uncovered. If `board.Add` fails (duplicate ID from race), that path is uncovered too.
- **What's missing:** The `ValidateAdd` error path and the `Add` error path inside `registerTask`.
- **Recommendation:** Test submitting a task that would create a dependency cycle via the postoffice. Test submitting a duplicate task ID.

### Medium Risk

#### `taskboard.Save` -- 55.0%
- **Classification:** I/O Boundary (temp file, write, rename)
- **Why medium:** Atomic write pattern. The uncovered paths are error handling: `tmp.Write` failure, `tmp.Close` failure, `os.Rename` failure. These are hard to trigger in normal tests but matter for disk-full or permission-denied scenarios.
- **What's missing:** Error paths for write failure, close failure, rename failure.
- **Recommendation:** Consider a test with a read-only directory to trigger the `CreateTemp` error path. The write/close/rename errors are acceptable skip candidates (would require fault injection).

#### `taskboard.Load` -- 72.7%
- **Classification:** I/O Boundary (ReadFile + Unmarshal)
- **Why medium:** The uncovered path is a non-NotExist read error (e.g., permission denied). The `b.Tasks == nil` initialization branch after unmarshal is also partially uncovered.
- **Recommendation:** Test with a file that has `"tasks": null` in JSON to exercise the nil-map guard.

#### `expert.WriteState` -- 61.9%
- **Classification:** I/O Boundary (file writes, merge logic)
- **Why medium:** Handles state.md persistence with merge semantics. Uncovered paths include the JSON unmarshal error path and the write error path.
- **Recommendation:** Test with malformed existing state.md to exercise parse error recovery.

#### `expert.WriteLog` -- 71.4%
- **Classification:** I/O Boundary (file write to logs/)
- **Why medium:** Log file write errors are uncovered. These are the "disk full" scenarios.
- **Recommendation:** Acceptable to skip; covered structurally by daemon integration tests.

#### `expert.AppendIndex` -- 72.2%
- **Classification:** I/O Boundary (append to TSV index file)
- **Why medium:** The error path for writing the index file is uncovered. The read + append flow is tested.
- **Recommendation:** Low-value to test directly; daemon integration tests exercise the happy path.

#### `expert.WriteStderr` -- 71.4%
- **Classification:** I/O Boundary (stderr log write)
- **Why medium:** Same pattern as WriteLog. Error path uncovered.
- **Recommendation:** Skip (same rationale as WriteLog).

#### `daemon.processInboxMessage` -- 67.7%
- **Classification:** State Machine (parse, check status, spawn, log, cleanup)
- **Why medium:** Large function with many branches. The uncovered paths include: MCP config write failure, blocked task skip, cancelled task skip, completed/failed task skip.
- **Recommendation:** Blocked/cancelled skip paths are exercised by `TestDaemon_BlockedTaskNotSpawned` and `TestDaemon_CancelPendingTask` indirectly. The MCP config failure path could use a targeted test.

#### `daemon.drainInbox` -- 72.0%
- **Classification:** State Machine (iterative drain with re-scan)
- **Why medium:** The `ReadDir` error path and the context-cancelled early return are uncovered.
- **Recommendation:** Context cancellation path is exercised by shutdown tests. ReadDir error would require injecting a broken filesystem.

#### `daemon.drainPostoffice` -- 70.0%
- **Classification:** State Machine (drain pre-existing messages)
- **Why medium:** The `.routing-` prefix skip and the `ReadDir` error return are uncovered.
- **Recommendation:** Add test with a `.routing-` prefixed file in the postoffice to verify it's skipped.

#### `mail.copyFile` -- 60.0%
- **Classification:** I/O Boundary (read file, write file)
- **Why medium:** The write error path is uncovered.
- **Recommendation:** Low-value; the happy path is well-exercised by routing tests.

### Low Risk / Skip

#### `expert.readOptionalFile` -- 85.7%
- **Classification:** I/O Boundary (trivial delegation: ReadFile with IsNotExist check)
- **Skip rationale:** Single branch, callee behavior. Error beyond not-exist is OS-level.

#### `expert.ReadState` -- 70.0%
- **Classification:** I/O Boundary (reads identity.md, state.md, errors.md)
- **Skip rationale:** Delegates to `readOptionalFile` three times. Tested indirectly via `AssemblePrompt`.

#### `daemon.resolveProjectDir` -- 60.0%
- **Classification:** Pure Logic (tilde expansion)
- **Note:** Low complexity but 60% coverage is surprising. The uncovered path is when `UserHomeDir` fails.
- **Skip rationale:** Single if-branch for tilde. Tilde expansion is tested in config.

#### `daemon.resolveExpertName` -- 80.0%
- **Classification:** Pure Logic (path comparison)
- **Skip rationale:** Simple loop with path comparison. Covered by integration tests.

#### `daemon.NewWatcher` / `watcher.Add` / `watcher.resolveDir` / `watcher.waitForStable` -- 75-80%
- **Classification:** I/O Boundary (fsnotify wrapper)
- **Skip rationale:** Thin wrappers over fsnotify. Integration-tested by daemon suite.

#### `defaultSpawner.Spawn` -- 0.0%
- **Classification:** Trivial delegation (calls `expert.Spawn`)
- **Skip rationale:** Single-line delegation. Testing would just test the delegation itself.

---

## Quality Issues by Priority

### P1 -- Flakiness Risk

**Sleep-based polling in daemon tests**
- **Files:** `internal/daemon/daemon_test.go`
- **Pattern:** Every daemon test uses `time.Sleep(500ms)` for startup, then polling loops with `time.Sleep(50-100ms)` and 5-second deadlines.
- **Impact:** On slow CI machines or under load, the 500ms startup sleep may not be enough. The test suite already takes 28 seconds due to accumulated sleeps.
- **Specific instances:**
  - Lines 118, 244, 315, 397, 486, 634, 697, 852, 918, 1017, 1089, 1187, 1246 -- all `time.Sleep(500*time.Millisecond)` for daemon startup
  - Lines 152-157, 251-257, 340-346 etc. -- polling loops with 100ms ticks
- **Recommendation:** Replace startup sleeps with a readiness signal (e.g., the daemon publishes to a channel when the watcher is running). For polling, consider exposing a `board` accessor or using a condition variable with the taskboard save.

### P2 -- Assertion Quality

**Existence-only assertions on taskboard fields (daemon tests)**
- **Files:** `internal/daemon/daemon_test.go`
- **Pattern:** Several tests check `task.Status` but not other fields like `From`, `Type`, or `Priority`. For example, `TestDaemon_TaskStatusLifecycle` checks `Status`, `ExitCode`, `StartedAt`, `CompletedAt` but not `Expert`, `From`, or `Type`.
- **Impact:** A bug that corrupts metadata fields during status transitions would pass all current tests.
- **Recommendation:** Add field assertions for `Expert`, `From`, `Type` in lifecycle tests.

**No error message assertions in taskboard tests**
- **Files:** `internal/taskboard/taskboard_test.go`
- **Pattern:** Error tests check `err == nil` / `err != nil` but never inspect the error message. For example, `TestBoard_AddDuplicate` and `TestBoard_AddEmptyID` just check for non-nil error.
- **Impact:** If the error message changes to something misleading, tests won't catch it.
- **Recommendation:** Add `strings.Contains` checks on error messages for the most important error paths (duplicate ID, empty ID, not found).

### P2 -- Table Test Structure

**ParseSessionTimeout could be table-driven**
- **File:** `internal/config/config_test.go`
- **Pattern:** `TestDefaultsSection_ParseSessionTimeout` tests three cases sequentially in one function (valid "10m", valid "30s", invalid "invalid"). Each case is a separate arrange-act-assert block.
- **Impact:** If the first case fails, the second and third never run.
- **Recommendation:** Convert to table-driven test with `t.Run` subtests.

### P3 -- Test Organization

**Daemon test setup duplication**
- **File:** `internal/daemon/daemon_test.go`
- **Pattern:** Every test function repeats the same ~15 lines of pool.toml creation, config loading, daemon construction, and goroutine launch. There are 12 test functions with near-identical setup.
- **Impact:** Maintenance burden. If the setup changes (e.g., new required config field), all 12 tests need updating.
- **Recommendation:** Extract a `testDaemon` helper that returns `(daemon, fakeSpawner, cancel, errCh)` with sensible defaults and cleanup.

**Compose test missing `InvalidID` case**
- **File:** `internal/mail/compose_test.go`
- **Pattern:** Compose validates IDs (path separators, `.`, `..`) but there's no test for `Compose` with a path-separator ID like `../../etc/passwd`.
- **Impact:** The validation exists in the code but is only tested via `Parse`, not `Compose`.
- **Recommendation:** Add `TestCompose_InvalidID` with path traversal attempt.

---

## Recommended Next Steps

1. **[High] Test `expert.Spawn` error paths** -- Extract cmd-building into a testable function. Test env var injection, exit code extraction with a fake binary. This is the highest-risk untested code.

2. **[High] Add daemon tests for cycle-rejection and duplicate-task paths** -- Submit a task via postoffice that would create a dependency cycle. Verify the daemon rejects it and the board is unchanged.

3. **[Medium] Replace daemon test sleeps with readiness signals** -- The 28-second test suite is dominated by sleep accumulation. A readiness channel would cut runtime significantly and reduce flakiness risk.

4. **[Medium] Extract daemon test setup into a helper** -- Reduce the ~180 lines of duplicated setup across 12 tests into a reusable `testDaemon()` function.

5. **[Medium] Add error message assertions to taskboard tests** -- Especially for `Add` (duplicate, empty ID) and `Update` (not found). These are the most user-visible error paths.

6. **[Low] Test `taskboard.Load` with null tasks JSON** -- Exercise the `b.Tasks == nil` guard after unmarshal.

7. **[Low] Test `.routing-` file skip in `drainPostoffice`** -- Simple to add, verifies an important edge case for crash recovery.

8. **[Low] Convert `ParseSessionTimeout` test to table-driven** -- Minor cleanup for consistency.
