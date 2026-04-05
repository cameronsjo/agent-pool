# v0.6: Daemon Lifecycle + Observability

## Context

Dogfooding on bosun exposed that operating a pool is painful: no way to stop the daemon cleanly, no visibility into what's happening, no lifecycle management. v0.6 adds a Unix domain socket for CLI->daemon communication, enabling `stop`, `status`, and `watch` commands. Also addresses session timeout defaults and the `pool_` tool name prefix. Issues: #8, #9, #10, #11, #13.

---

## Phase 1: Socket + Stop

**New:** `internal/daemon/socket.go`
- `socketServer` struct: `net.Listener`, `*Daemon`, `context.CancelFunc`
- `newSocketServer(sockPath, daemon, cancel)` — removes stale socket, listens
- `serve(ctx)` — accept loop, one goroutine per connection
- `handleConn(conn)` — 5s read deadline, reads one NDJSON line, dispatches by method, writes response, closes
- `close()` — closes listener, removes socket file
- Protocol types: `socketRequest{Method}`, `socketResponse{Status, Data, Message}`
- Methods: `stop` (calls cancel), `status` (placeholder), `subscribe` (placeholder)

**New:** `internal/daemon/socket_test.go`
- `TestSocket_StopMethod` — start daemon, connect, send stop, verify exit
- `TestSocket_UnknownMethod` — error response, daemon stays up
- `TestSocket_StaleSocketCleanup` — pre-existing socket file doesn't block start
- `TestSocket_SocketRemovedOnShutdown` — file cleaned up after exit
- Helper: `connectSocket(t, poolDir) net.Conn`

**Modify:** `internal/daemon/daemon.go`
- Add `startedAt time.Time`, `sockPath string` to Daemon struct
- In `Run()`: wrap ctx in child context, share cancel with socket server
  ```
  childCtx, cancel := context.WithCancel(ctx)
  sock := newSocketServer(sockPath, d, cancel)
  go sock.serve(childCtx)
  ```
- Socket stop -> cancel() -> childCtx.Done() -> event loop exits

**Modify:** `cmd/agent-pool/main.go`
- Add `"stop"` to switch, implement `cmdStop()`
- `cmdStop`: discover pool dir, `net.Dial("unix", daemon.sock)`, send `{"method":"stop"}`, print result
- Extract `connectAndSend(sockPath, method) (*socketResponse, error)` helper
- Update `printUsage()`, bump version to `v0.6.0-dev`

**Commit:** `feat: add unix domain socket and stop command`

---

## Phase 2: Graceful Drain

**Modify:** `internal/daemon/daemon.go`
- Add `wg sync.WaitGroup` to Daemon struct
- Add `drainTimeout time.Duration` (default 30s) + `WithDrainTimeout` option
- Wrap ALL 5 goroutine dispatch sites with `wg.Add(1)` / `defer wg.Done()`:
  1. `handleApprovalRequest` dispatch (Run event loop, ~line 170)
  2. `handleInbox` dispatch (Run event loop, ~line 179)
  3. Architect drain in `drainAllInboxes` (~line 729)
  4. Expert drains in `drainAllInboxes` (~line 731-733)
  5. Wake experts in `markTaskCompleted` (~line 704)
- Replace shutdown path: cancel -> wg.Wait(30s timeout) -> close socket -> return

**Modify:** `cmd/agent-pool/main.go`
- Double-signal handler: first signal cancels ctx (graceful), second signal calls `os.Exit(1)` (immediate)

**Modify:** `internal/daemon/daemon_test.go`
- `TestDaemon_GracefulDrainWaitsForInFlight` — gated spawner, cancel while blocked, verify wait then clean exit

**Commit:** `feat: add graceful drain with WaitGroup and double-signal`

---

## Phase 3: Status

**Modify:** `internal/daemon/socket.go`
- Define `statusData` struct: Pool, State, Uptime, Experts, TaskCounts, ActiveTasks
- Define `activeTaskInfo`: ID, Expert, Started

**Modify:** `internal/daemon/daemon.go`
- Add `Status() statusData` method — reads board under mu, computes counts via `TasksByStatus()`, collects active tasks, expert names from cfg

**Modify:** `internal/daemon/socket.go`
- Wire status method to return `d.daemon.Status()` as response data

**Modify:** `cmd/agent-pool/main.go`
- Add `"status"` to switch, implement `cmdStatus()`
- Pretty-print: pool name, state, uptime, expert list, task count table, active task list

**Modify:** `internal/daemon/socket_test.go`
- `TestSocket_StatusMethod` — verify response has pool name, experts, counts
- `TestSocket_StatusWithActiveTasks` — gated spawn, verify active task entry

**Commit:** `feat: add status command and socket status method`

---

## Phase 4: Watch (Event Streaming)

**New:** `internal/daemon/events.go`
- Event types: `task.routed`, `expert.spawning`, `expert.completed`, `expert.failed`, `task.cancelled`, `task.unblocked`
- `Event` struct: Type, Timestamp, Data (typed per event)
- `eventBus` struct: `sync.RWMutex`, subscriber map of `id -> chan Event`
- `subscribe() (id, <-chan Event)` — buffered channel (cap 64)
- `unsubscribe(id)` — removes + closes channel
- `emit(Event)` — non-blocking send under read lock (drop if full)

**New:** `internal/daemon/events_test.go`
- `TestEventBus_SubscribeReceivesEvents`
- `TestEventBus_MultipleSubscribers`
- `TestEventBus_UnsubscribeCleansUp`
- `TestEventBus_SlowSubscriberDropsEvents`

**Modify:** `internal/daemon/daemon.go`
- Add `events *eventBus` to struct, init in `New()`
- Add `emit(EventType, data)` helper
- Insert emit calls at 6 existing log points:
  1. `handlePostoffice` after successful route -> `task.routed`
  2. `processInboxMessage` before spawn -> `expert.spawning`
  3. `processInboxMessage` on success -> `expert.completed`
  4. `processInboxMessage` on failure -> `expert.failed`
  5. `handleCancel` on cancel -> `task.cancelled`
  6. `markTaskCompleted` for unblocked tasks -> `task.unblocked`

**Modify:** `internal/daemon/socket.go`
- `handleSubscribe(conn, ctx)` — subscribe to bus, stream events as NDJSON, unsubscribe on disconnect/cancel

**Modify:** `cmd/agent-pool/main.go`
- Add `"watch"` to switch, implement `cmdWatch()`
- Connect, send subscribe, read NDJSON stream
- ANSI color per event type: green=completed, red=failed, yellow=spawning, cyan=routed
- Signal handler for clean disconnect on Ctrl-C

**Modify:** `internal/daemon/socket_test.go`
- `TestSocket_SubscribeStreamsEvents` — subscribe, route message, verify event arrives
- `TestSocket_SubscribeDisconnectCleansUp` — close client, verify no leak

**Commit:** `feat: add event bus and watch command`

---

## Phase 5: Timeout Removal + Tool Rename

### 5a: Session timeout optional

**Modify:** `internal/config/config.go`
- Remove `session_timeout = "10m"` default from `LoadPool()` (~line 153)
- `ParseSessionTimeout()` returns `(0, nil)` when string is empty

**Modify:** `internal/daemon/daemon.go`
- In `processInboxMessage`: if timeout == 0, use `context.WithCancel(ctx)` instead of `context.WithTimeout`
- Update `resolveSessionTimeout` to return 0 for empty values

**Commit:** `feat: make session_timeout optional (zero = no timeout)`

### 5b: Drop pool_ prefix

Mechanical rename across all files. Tool names: `pool_read_state` -> `read_state`, etc.

| File | Changes |
|------|---------|
| `internal/mcp/config.go` | ExpertToolNames (6 entries) |
| `internal/mcp/tools.go` | 6 NewTool calls |
| `internal/mcp/architect_tools.go` | 4 NewTool calls |
| `internal/mcp/concierge_tools.go` | 6 NewTool calls + description refs |
| `internal/mcp/tools_test.go` | ~10 callTool refs |
| `internal/mcp/architect_tools_test.go` | ~30 refs |
| `internal/mcp/concierge_tools_test.go` | ~35 refs |
| `plugin/concierge-identity.md` | 4 refs |
| `plugin/skills/pool-ask.md` | 2 refs |
| `plugin/skills/pool-build.md` | 3 refs |
| `plugin/skills/pool-status.md` | 1 ref |
| External: bosun concierge identity | 4 refs |

**Commit:** `refactor: drop pool_ prefix from MCP tool names`

---

## Phase 6: launchd

**New:** `scripts/com.agent-pool.daemon.plist` — template with `AGENT_POOL_BINARY` and `POOL_DIR` placeholders

**New:** `docs/launchd.md` — install/load/unload instructions

**Commit:** `docs: add launchd plist template and install guide`

---

## Verification

After each phase:
```bash
make test        # all tests pass
make build       # binary builds
```

End-to-end after all phases:
```bash
# Terminal 1: start daemon
bin/agent-pool start ~/.agent-pool/pools/test

# Terminal 2: check status
bin/agent-pool status

# Terminal 3: watch events
bin/agent-pool watch

# Terminal 2: stop daemon
bin/agent-pool stop
# Verify daemon exits cleanly in terminal 1
```

## Risks

- **Phase 2 WaitGroup**: Exactly 5 goroutine dispatch sites. Missing one = goroutine leak bypassing drain. Mitigate: grep for `go d.` and `go func` after implementation.
- **Phase 4 subscriber leak**: Disconnected client fills channel. Mitigate: non-blocking emit (select+default), write errors break serve loop.
- **Phase 5 rename**: Breaking change for bosun. Mitigate: update bosun in same session.
