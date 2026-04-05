# v0.6 — Daemon Lifecycle + Observability

## Where We Are

v0.5 is merged on main. The system now has a full user-facing integration:
concierge MCP tools (dispatch, collect, ask_expert, submit_plan, check_status,
list_experts), a Claude Code plugin scaffold with skills, and read/write path
flows that work end-to-end.

Dogfooding on bosun (2026-04-05) validated the core — parallel expert dispatch
is the killer feature. But it also exposed that operating a pool is painful:
no way to stop the daemon cleanly, no visibility into what's happening, no
lifecycle management at all.

Six bugs were fixed on main during dogfooding (no PR, direct to main):
- `--verbose` flag for claude stream-json print mode
- MCP tool names auto-appended to `--allowedTools` for headless sessions
- `pool_dispatch` + `pool_collect` (non-blocking expert dispatch)
- Pool dir auto-discovery (walks up from cwd for `.agent-pool/`)
- All Debug logging promoted to Info
- Daemon logs tee to `{poolDir}/daemon.log`

## What v0.6 Adds

Per docs/plans/architecture.md § Implementation Phasing:

▎ Operational foundation surfaced by dogfooding.

Scope:
- Unix domain socket for CLI→daemon communication (`daemon.sock`)
- `agent-pool stop` — connect to socket, send shutdown RPC
- `agent-pool status` — connect to socket, return live stats
- `agent-pool watch` — stream events via socket, render TUI dashboard
- Graceful shutdown with drain (sync.WaitGroup, 30s timeout, double-signal)
- Remove default session timeout — sessions run to completion
- Drop `pool_` prefix from MCP tool names
- launchd plist template for macOS backgrounding

Issues: #8, #9, #10, #11, #13

Validates: Can you operate a pool without staring at raw JSON logs? Can you
stop/restart without pkill?

## Key Research Context

### Industry Consensus (from background research agent)

Every major Go daemon (Docker, Consul, Nomad, containerd, kubelet) follows
the same pattern:

1. **Run foreground, don't fork.** Go's multithreading breaks fork+exec.
   systemd/launchd handle backgrounding.
2. **Unix socket for CLI→daemon.** No pidfile needed — socket liveness IS
   the health check. Connection refused = daemon not running.
3. **SIGTERM = graceful drain.** Stop accepting new work, wait for in-flight
   (30s timeout), exit. Second signal = immediate exit.
4. **No SIGHUP yet** — config reload is a future feature (v0.9).

Libraries considered:
- `net.Listen("unix", path)` + `encoding/json` — sufficient, no deps needed
- `kardianos/service` — cross-platform service registration (future, not v0.6)
- `coreos/go-systemd` — sd_notify for Linux (future, not v0.6)

### Socket Protocol

Simple newline-delimited JSON over unix socket. No HTTP, no gRPC — overkill
for a local CLI→daemon channel.

Request: `{"method": "status"}` or `{"method": "stop"}` or `{"method": "subscribe"}`
Response: `{"status": "ok", "data": {...}}` or stream of events for subscribe.

### Event Types for `watch`

The daemon already logs at every state transition. Events are emitted at the
same points, just structured for the socket subscriber instead of slog:

| Event | Data |
|-------|------|
| `task.routed` | id, from, to, type, body preview (100 chars) |
| `expert.spawning` | expert, task_id, model |
| `expert.completed` | expert, task_id, duration, exit_code, summary |
| `expert.failed` | expert, task_id, duration, exit_code |
| `task.cancelled` | task_id, cancel_note |
| `task.unblocked` | task_id, expert |

## What Already Exists

**Daemon (internal/daemon/daemon.go):**
- `Run()` blocks on fsnotify event loop, returns on context cancellation
- `handleInbox` and `handleApprovalRequest` dispatched as goroutines (no WaitGroup yet)
- Logs to both stdout and `{poolDir}/daemon.log` via `io.MultiWriter`
- Signal handling via `signal.NotifyContext` in cmdStart

**CLI (cmd/agent-pool/main.go):**
- `cmdStart()` loads config, creates daemon, runs until signal
- `DiscoverPoolDir()` walks up from cwd to find `.agent-pool/`
- `parseFlags()` extracts `--flag value` pairs from args

**MCP tools (internal/mcp/):**
- `ExpertToolNames` in config.go lists all tool names for `--allowedTools`
- Tool names currently use `pool_` prefix (to be dropped)
- `postMessage()` helper handles compose + atomic write to postoffice

**Taskboard (internal/taskboard/):**
- `Load()` reads atomically (handles missing file gracefully)
- `Board.Save()` uses `atomicfile.WriteFile` for safe persistence
- Task status lifecycle: pending → blocked → active → completed/failed/cancelled

**Gap: No socket infrastructure.** The daemon has no listener, no RPC handler,
no event bus. All CLI→daemon communication is via filesystem (writing mail
files to postoffice, reading taskboard.json).

**Gap: No WaitGroup for goroutines.** `handleInbox` runs in goroutines that
outlive `Run()` on shutdown. Already caused TempDir cleanup race in tests
(fixed with `waitForTaskStatus` workaround). Production needs real drain.

**Gap: Session timeout still hardcoded.** `processInboxMessage` wraps spawn
context with `context.WithTimeout(ctx, timeout)`. Needs to be made optional.

## What to Read First

1. docs/plans/architecture.md — §v0.6 (the scope definition)
2. docs/field-reports/2026-04-05-first-dogfood.md — what broke and why
3. internal/daemon/daemon.go — Run(), handleInbox goroutine dispatch, signal handling
4. cmd/agent-pool/main.go — cmdStart(), signal setup, log file creation
5. internal/daemon/daemon_test.go — fakeSpawner, waitForTaskStatus, shutdownDaemon patterns
6. GitHub issues #8, #9, #10, #11, #13 — detailed designs with research

## Approach Suggestion

The socket is the backbone — everything else builds on it. Recommended order:

**Phase 1: Socket + Stop**
Add unix socket listener to `daemon.Run()`. Implement `stop` method that
cancels the daemon context. Add `cmdStop` that connects and sends stop.
This gives you clean shutdown immediately.

**Phase 2: Graceful Drain**
Add `sync.WaitGroup` to daemon for tracking `handleInbox`/`handleApproval`
goroutines. On shutdown: cancel context → wg.Wait(30s) → close socket → exit.
Add double-signal handler (second SIGTERM = immediate exit).

**Phase 3: Status**
Add `status` method to socket server. Returns pool name, expert list, uptime,
active/completed/failed task counts from taskboard. Add `cmdStatus`.

**Phase 4: Watch (Event Streaming)**
Add event bus to daemon — goroutines emit events at existing log points.
Socket `subscribe` method streams events as NDJSON. Add `cmdWatch` with
a simple ANSI renderer (bubbletea TUI can come later).

**Phase 5: Timeout Removal + Tool Rename**
Remove `context.WithTimeout` from spawn. Make `session_timeout` optional
(zero value = no timeout). Rename all `pool_*` tools to drop prefix.
Update `ExpertToolNames`, plugin skills, bosun rules.

**Phase 6: launchd**
Ship `scripts/com.agent-pool.daemon.plist` template. Document install:
`cp plist ~/Library/LaunchAgents/ && launchctl load ...`

## Design Questions to Resolve

1. **Socket path:** `{poolDir}/daemon.sock` (scoped to pool, like our original
   pidfile plan) vs `/tmp/agent-pool-{pool-name}.sock` (avoids long paths)?
   Recommendation: `{poolDir}/daemon.sock` — consistent with everything else.

2. **Socket protocol:** NDJSON (simple, no deps) vs JSON-RPC 2.0 (structured,
   has error codes)? Recommendation: NDJSON — we don't need the ceremony of
   JSON-RPC for 3 methods.

3. **Event bus implementation:** Channel-based (daemon pushes to subscribers)
   vs poll-based (watch command reads taskboard)? Recommendation: channels —
   the daemon already has the events, just needs to fan them out.

4. **bubbletea for TUI or raw ANSI?** bubbletea adds a dependency but gives
   proper terminal handling. Raw ANSI is zero-dep but fragile.
   Recommendation: start with raw ANSI clear+print loop, upgrade to bubbletea
   if it's not enough. Don't add deps before validating the UX.

5. **Tool rename migration:** Breaking change for existing .claude/rules/ files
   and plugin skills that reference `pool_*` names. Do we keep aliases?
   Recommendation: clean break. Update all references in one commit. The pool_
   prefix only existed for ~1 day in production.
