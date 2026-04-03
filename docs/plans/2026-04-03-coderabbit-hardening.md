# CodeRabbit PR Review Fixes — v0.1 Hardening

## Context

PR #1 (`feat/v0.1-expert-lifecycle`) received 15 findings from CodeRabbit. After triaging against the architecture doc (`docs/plans/architecture.md` and the Obsidian vault's `Agent Pool - Architecture Plan.md`), 8 findings warrant fixes. One is skipped as v0.2+ scope (MessageType validation). The fixes harden v0.1 without adding scope beyond the MVP.

Key architecture constraints informing these fixes:
- **At-least-once delivery** — duplicates expected, experts should be idempotent
- **No backpressure** — mail queues in inbox, FIFO, one task at a time per expert
- **Handoff is first-class** — non-zero exit isn't always failure (v0.2+ distinguishes via MCP)
- **Dead-letter/retry is v0.3 scope** (task board + dependencies) — v0.1 skips broken files

## Fixes

### Fix 1: Validate message ID is filename-safe (`internal/mail/mail.go`)

**What:** `msg.ID` is used as a filename in routing (`msg.ID+".md"`) and logging (`taskID+".json"`). A malicious ID like `../../etc/passwd` enables path traversal.

**Where:** Add validation in `Parse()` after unmarshalling, alongside the existing required-field checks (line ~90).

**How:**
```go
if msg.ID != filepath.Base(msg.ID) || msg.ID == "." || msg.ID == ".." {
    return nil, fmt.Errorf("invalid message ID %q: must be a simple filename", msg.ID)
}
```

Add `"path/filepath"` to imports. This protects both `mail.Route` and `expert.WriteLog`/`AppendIndex` — validation at the parse boundary means no downstream code needs its own check.

**Tests:** Add cases to `TestParse_MissingRequiredField` table: ID with path separator, ID of `.`, ID of `..`.

---

### Fix 2: Convert recursive drainInbox to iterative loop (`internal/daemon/daemon.go`)

**What:** `handleInbox` defers `drainInbox` which calls `handleInbox` — recursive. Stack grows one frame per queued message. Infinite loop if a file can't be parsed/removed.

**Where:** `daemon.go` lines 129-264

**How:** Restructure so `handleInbox` does NOT call `drainInbox`. Instead, extract the single-task processing logic into a `processInboxMessage` method. Then `drainInbox` becomes an iterative loop:

```go
func (d *Daemon) drainInbox(ctx context.Context, expertName string) {
    for {
        next := d.nextInboxFile(expertName)
        if next == "" {
            return
        }
        d.processInboxMessage(ctx, expertName, next)
    }
}
```

`processInboxMessage` contains the current `handleInbox` body (parse, spawn, log, remove) without the busy-flag logic or drain recursion. The busy flag stays in `handleInbox` which is called from the event loop — it sets busy, calls `drainInbox` (iterative), then clears busy.

**Broken file handling:** If `ParseFile` fails, log the error and `continue` (skip the file). Don't remove it — it stays in the inbox for manual inspection. This avoids infinite retry without needing a dead-letter queue (v0.3 scope).

---

### Fix 3: Don't treat non-zero exit as success (`internal/daemon/daemon.go`)

**What:** `expert.Spawn` returns `result` with `err == nil` for `exec.ExitError` (non-zero exit). Current code still writes logs, removes inbox file, and logs "Task completed".

**Where:** `handleInbox` / `processInboxMessage` — the success path after `Spawn` returns.

**How:**
- **Always** write to `logs/` and append to index (the archive is append-only by design — we want the output even from failures)
- **Always** include exit code in the index entry and log message
- On `result.ExitCode != 0`:
  - Log at `Warn` level: "Expert session failed" with exit code
  - **Do NOT** remove the inbox file (it becomes a natural retry candidate on restart)
- On `result.ExitCode == 0`:
  - Log at `Info` level: "Task completed"
  - Remove the inbox file

**Design note:** The architecture says handoff will be a distinct signal (v0.2+ via MCP). For v0.1, non-zero exit = don't acknowledge completion. The message persists in inbox.

**LogEntry change:** Add `ExitCode int` field to `LogEntry` struct and include it in the index table row.

---

### Fix 4: Drain postoffice at startup (`internal/daemon/daemon.go`)

**What:** `drainAllInboxes` runs at startup but postoffice is never drained. Pre-existing messages sit unrouted.

**Where:** `daemon.go` `Run()` method, before `drainAllInboxes`.

**How:** Add `drainPostoffice` method:

```go
func (d *Daemon) drainPostoffice(ctx context.Context) {
    postofficeDir := filepath.Join(d.poolDir, "postoffice")
    entries, err := os.ReadDir(postofficeDir)
    if err != nil {
        return
    }
    for _, entry := range entries {
        if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
            continue
        }
        if strings.HasPrefix(entry.Name(), ".routing-") {
            continue
        }
        d.handlePostoffice(ctx, filepath.Join(postofficeDir, entry.Name()))
    }
}
```

Call in `Run()`: `d.drainPostoffice(ctx)` then `d.drainAllInboxes(ctx)`.

---

### Fix 5: Return error when file never stabilizes (`internal/daemon/watcher.go`)

**What:** `waitForStable` returns `nil` after max attempts even if size was still changing. Truncated files get parsed.

**Where:** `watcher.go` line ~165

**How:** Change the final return from `return nil` to:

```go
return fmt.Errorf("file size still changing after %d attempts", stabilityAttempts)
```

This causes the watcher to log "File not stable, skipping" and drop the event. The file stays on disk. If it was a real write that just took too long, the daemon will pick it up on next restart via the drain pass (Fix 4 / existing `drainAllInboxes`).

---

### Fix 6: Don't log raw stderr (`internal/expert/expert.go`)

**What:** Full stderr from Claude is logged to the shared logger. May contain prompt fragments, file paths, or credentials.

**Where:** `expert.go` lines 180-186

**How:**
- Log only metadata: stderr length and whether it's non-empty
- Persist full stderr to `logs/{task-id}.stderr` alongside the JSON output
- Update `WriteLog` to accept an optional stderr parameter (or add a separate `WriteStderr` function)

```go
if len(stderr.Bytes()) > 0 {
    logger.Warn("Expert session produced stderr output",
        "expert", cfg.Name,
        "task_id", cfg.TaskMessage.ID,
        "stderr_bytes", len(stderr.Bytes()),
    )
}
```

---

### Fix 7: Handle `os.Getwd()` error (`cmd/agent-pool/main.go`)

**Where:** Line 50

**How:**
```go
poolDir, err = os.Getwd()
if err != nil {
    fmt.Fprintf(os.Stderr, "error getting current directory: %v\n", err)
    os.Exit(1)
}
```

---

### Fix 8: Handle all `os.Stat` errors (`internal/mail/router.go`)

**Where:** Line 46

**How:** Change from:
```go
if _, err := os.Stat(inboxDir); os.IsNotExist(err) {
```
To:
```go
if _, err := os.Stat(inboxDir); err != nil {
```

Both "doesn't exist" and "permission denied" should prevent routing.

---

## Files Modified

| File | Fixes | Risk |
|------|-------|------|
| `internal/mail/mail.go` | #1 (ID validation) | Low — additive validation |
| `internal/mail/mail_test.go` | #1 (test cases) | Low |
| `internal/daemon/daemon.go` | #2, #3, #4 (drain refactor, exit code, postoffice) | Medium — structural change to handleInbox/drainInbox |
| `internal/daemon/daemon_test.go` | #2, #3, #4 (tests) | Low |
| `internal/daemon/watcher.go` | #5 (stability check) | Low — one-line change |
| `internal/expert/expert.go` | #6 (stderr logging) | Low |
| `internal/expert/log.go` | #3 (ExitCode in LogEntry) | Low |
| `internal/expert/expert_test.go` | #3, #6 (tests) | Low |
| `cmd/agent-pool/main.go` | #7 (Getwd error) | Low — one-liner |
| `internal/mail/router.go` | #8 (Stat check) | Low — one-liner |

## Execution Order

Fixes are independent except #2 and #3 share the `handleInbox`/`processInboxMessage` refactor. Group them:

1. **Agent A** — Fixes #2, #3, #4 (daemon.go refactor + tests) — these are coupled
2. **Agent B** — Fix #1 (mail.go validation + tests)
3. **Agent C** — Fixes #5, #6 (watcher + expert stderr)
4. **Direct** — Fixes #7, #8 (one-liners in main.go and router.go)

## Verification

1. `go test ./... -count=1` — all existing + new tests pass
2. `go vet ./...` — no warnings
3. `scripts/coverage-gaps.sh 70` — no regressions
4. Manual: write a message with `id: ../../etc/test` to postoffice — should be rejected at parse time
5. Manual: write a message to postoffice before starting daemon, start daemon — should be routed
