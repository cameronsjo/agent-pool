# v0.3 — Task Board + Dependencies

## Context

v0.2 is merged on `main`. Experts have MCP tools and hooks, but the daemon has no durable task tracking — it uses an in-memory `busy` map and relies on inbox file presence for state. v0.3 offloads bookkeeping from the LLM: the daemon maintains `taskboard.json` with task status, dependency evaluation, cancellation, handoff tracking, health checking, and process supervision.

## New Package: `internal/taskboard/`

Separate package for task state, persistence, and DAG evaluation. The daemon orchestrates; the taskboard is a data structure with pure-function evaluation. This keeps the DAG evaluator unit-testable without fsnotify or fakeSpawner setup.

## Implementation Steps

### Step 1: Taskboard CRUD + Persistence

**New files:**
- `internal/taskboard/taskboard.go` — Board/Task types, status constants, CRUD
- `internal/taskboard/store.go` — Load/Save (atomic temp+rename)
- `internal/taskboard/taskboard_test.go`

**Types:**

```go
type Status string // pending, blocked, active, completed, failed, cancelled

type Task struct {
    ID             string     `json:"id"`
    Status         Status     `json:"status"`
    Expert         string     `json:"expert"`
    PID            int        `json:"pid,omitempty"`
    DependsOn      []string   `json:"depends_on,omitempty"`
    From           string     `json:"from"`
    Type           string     `json:"type"`
    Priority       string     `json:"priority"`
    CreatedAt      time.Time  `json:"created_at"`
    StartedAt      *time.Time `json:"started_at,omitempty"`
    CompletedAt    *time.Time `json:"completed_at,omitempty"`
    HandoffCount   int        `json:"handoff_count"`
    NeedsAttention bool       `json:"needs_attention,omitempty"`
    CancelNote     string     `json:"cancel_note,omitempty"`
    ExitCode       *int       `json:"exit_code,omitempty"`
}

type Board struct {
    Version int              `json:"version"`
    Tasks   map[string]*Task `json:"tasks"`
}
```

**Functions:** `Load(path)`, `Save(path)`, `Add(task)`, `Get(id)`, `Update(id, fn)`, `TasksByStatus(status)`, `HasActive(expert) bool`

Board has no mutex — concurrency managed by daemon's existing `d.mu`.

**Tests:** Add/Get, duplicate rejection, Save/Load roundtrip, missing file returns empty board, TasksByStatus filter, Update mutation

---

### Step 2: DAG Evaluator

**New file:** `internal/taskboard/dag.go`

**Functions:**
- `EvaluateDeps() []string` — After completion: blocked→pending for tasks whose deps are all completed. Returns newly ready IDs. Failed deps propagate failure transitively.
- `DetectCycles() [][]string` — Kahn's algorithm at insert time. Rejects tasks that would create cycles.
- `DepsCompleted(id) bool` / `DepsFailed(id) bool` — predicate helpers

**Tests:** Simple dep chain, multi-dep, partial completion, cycle detection, self-cycle, failed dep propagation, diamond dependency, full lifecycle

---

### Step 3: Integrate Taskboard into Daemon

**Modified:** `internal/daemon/daemon.go`, `internal/daemon/daemon_test.go`

Changes to `Daemon` struct:
- Add `board *taskboard.Board` and `boardPath string`
- Remove `busy map[string]bool`

Changes to functions:
- **`New()`** — Load board from `{poolDir}/taskboard.json`
- **`handlePostoffice()`** — After routing a `type: task` message: register in taskboard (pending or blocked based on DependsOn), cycle-check, save
- **`handleInbox()`** — Replace `d.busy[expertName]` with `d.board.HasActive(expertName)`
- **`processInboxMessage()`** — Before spawn: check taskboard status under `d.mu` (skip if blocked/cancelled). Mark active. After spawn: mark completed/failed, call `EvaluateDeps()`, save board
- **`drainInbox()`** — If a message's ID isn't in the taskboard yet (pre-existing inbox file), register as pending before processing

All 5 existing daemon tests must continue passing — the taskboard is additive.

**New tests:** Taskboard created on start, task status lifecycle (pending→active→completed), busy enforcement via taskboard

---

### Step 4: Cancel Message Handling

**Modified:** `internal/daemon/daemon.go`, `internal/mail/mail.go`, `internal/mail/compose.go`

**New mail field:** `Cancels string` on Message and composeHeader (`yaml:"cancels,omitempty"`) — the task ID this cancel targets.

**Cancel flow in `handlePostoffice()`:**
1. Parse message, check `msg.Type == TypeCancel`
2. Look up `msg.Cancels` in taskboard
3. If pending/blocked: set cancelled, remove inbox file, run `EvaluateDeps()` to propagate, delete cancel message from postoffice (consumed by daemon, never delivered)
4. If active: record cancel_note, log warning, expert continues (no-op per architecture doc)
5. If already completed/failed/cancelled: no-op

**Cancel race mitigation:** `processInboxMessage()` re-checks taskboard status under `d.mu` before spawning. Cancel sets status under the same lock. Whichever acquires first wins.

**Tests:** Cancel pending task, cancel active task (no-op), cancel completed task (no-op), cancel race with spawn (gate channel), cancel propagates to dependents

---

### Step 5: Health Checking (Session Timeout)

**Modified:** `internal/config/config.go`, `internal/expert/expert.go`, `internal/daemon/daemon.go`

**Config:** Add `ParseSessionTimeout() (time.Duration, error)` to `DefaultsSection`

**Expert spawn change:** Replace `cmd.Run()` with `cmd.Start()` + goroutine `cmd.Wait()` + `select` on context cancellation. On timeout: send `SIGTERM`, grace period (10s), then `SIGKILL`. Add `PID int` to `Result`.

**Daemon change:** In `processInboxMessage()`, create child context with `context.WithTimeout(ctx, timeout)`, pass to `Spawn()`.

**Tests:** ParseSessionTimeout unit test, fakeSpawner respects context cancellation (short timeout triggers error), daemon marks timed-out task as failed

---

### Step 6: Handoff Tracking

**Modified:** `internal/daemon/daemon.go`, `internal/taskboard/taskboard.go`

**New Board method:** `RecordHandoff(taskID string)` — increments HandoffCount, sets NeedsAttention if count >= 2

**Daemon change in `handlePostoffice()`:** When `msg.Type == TypeHandoff`, look up active task for the sending expert, call `RecordHandoff()`, log warning on escalation.

**Tests:** Handoff increments count, escalation after 2 handoffs, unit test on Board.RecordHandoff

---

## Concurrency Model

All taskboard access goes through `d.mu` (the daemon's existing mutex). The Board has no internal locking. Lock is held for in-memory map ops + JSON file write (sub-millisecond for typical pools).

| Operation | Lock held | Mutation |
|-----------|-----------|----------|
| Register task (handlePostoffice) | d.mu | Add to board, Save |
| Cancel task (handlePostoffice) | d.mu | Update status, remove inbox, Save |
| Record handoff (handlePostoffice) | d.mu | Increment count, Save |
| Check busy (handleInbox) | d.mu | Read-only |
| Mark active (processInboxMessage) | d.mu | Update status, Save |
| Mark complete (processInboxMessage) | d.mu | Update status, EvaluateDeps, Save |

Cancel vs spawn race: both acquire `d.mu`. The re-check in `processInboxMessage` before spawning is the synchronization point.

## Files Changed

| File | Change |
|------|--------|
| `internal/taskboard/taskboard.go` | **New** — types, CRUD, status constants |
| `internal/taskboard/store.go` | **New** — Load/Save atomic persistence |
| `internal/taskboard/dag.go` | **New** — DAG evaluator, cycle detection |
| `internal/taskboard/taskboard_test.go` | **New** — unit tests for all above |
| `internal/daemon/daemon.go` | Replace busy map, integrate taskboard, cancel/handoff/timeout |
| `internal/daemon/daemon_test.go` | New tests for taskboard lifecycle, cancel, timeout, handoff |
| `internal/mail/mail.go` | Add `Cancels` field to Message |
| `internal/mail/compose.go` | Add `Cancels` to composeHeader |
| `internal/config/config.go` | Add `ParseSessionTimeout()` method |
| `internal/expert/expert.go` | Start/Wait pattern for SIGTERM, PID in Result |

## Verification

After each step:
1. `make test` — all existing + new tests pass
2. `make check` — vet + lint + test clean

After all steps:
1. `make test-cover` — check coverage of new code
2. Manual integration: start daemon, write a task with depends-on to postoffice, verify it blocks until dependency completes
3. Manual cancel: write cancel message, verify task removed from inbox
4. Timeout: configure 5s timeout, spawn a long task, verify SIGTERM fires
