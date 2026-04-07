# v0.7 Shared Experts Implementation Plan

## Context

Experts are currently pool-scoped — identity, state, logs all live under `{poolDir}/experts/{name}/`. This means a `security-standards` expert bootstrapped in one pool can't be reused in another without duplicating everything. v0.7 lifts experts to user scope (`~/.agent-pool/experts/`), with per-pool project overlays for project-specific state. Pools opt in via `shared.include` in pool.toml.

**Design decisions (resolved per v0.7 prompt):**
- Inbox: pool-scoped at `{poolDir}/shared-state/{name}/inbox/`
- Logs: pool-scoped at `{poolDir}/shared-state/{name}/logs/`
- Errors: user-level only at `~/.agent-pool/experts/{name}/errors.md`
- Multi-pool: deferred — separate `agent-pool start` processes per pool

## Phase 1: Shared Expert Resolution

### 1.1 — `config.SharedExpertDir` helper

**File:** `internal/config/config.go`

```go
func SharedExpertDir(name string) (string, error)
```

Returns `~/.agent-pool/experts/{name}/`. Validates name (no path separators, not empty, not `.`/`..`).

**Tests** (`internal/config/config_test.go`): happy path, path traversal rejection, empty name.

### 1.2 — Shared resolution functions in mail package

**File:** `internal/mail/router.go`

Add three new functions (existing `ResolveExpertDir`/`ResolveInbox` unchanged):

```go
func ResolveSharedExpertDir(name string) (string, error)  // → ~/.agent-pool/experts/{name}/
func ResolveSharedInbox(poolDir, name string) string       // → {poolDir}/shared-state/{name}/inbox/
func ResolveSharedLogDir(poolDir, name string) string      // → {poolDir}/shared-state/{name}/logs/
```

`ResolveSharedExpertDir` delegates to `config.SharedExpertDir`.

**Tests** (`internal/mail/router_test.go`): verify paths for each function.

### 1.3 — Config validation

**File:** `internal/config/config.go`

Add `Validate()` method on `PoolConfig`, called at end of `LoadPool`:
- `shared.include` names must not be builtin roles
- `shared.include` names must not overlap with `[experts.*]` keys
- Names must be filename-safe (no path separators)

**Tests** (`internal/config/config_test.go`): conflict with builtin, conflict with pool-scoped, path traversal, happy path.

### 1.4 — `ensureDirs` + `isSharedExpert` helper

**File:** `internal/daemon/daemon.go`

```go
func (d *Daemon) isSharedExpert(name string) bool
```

In `ensureDirs()`, iterate `d.cfg.Shared.Include` and create:
- `{poolDir}/shared-state/{name}/inbox/`
- `{poolDir}/shared-state/{name}/logs/`

**Test** (`internal/daemon/daemon_test.go`): verify dirs created for shared.include entries.

---

## Phase 2: Layered Prompt Assembly

### 2.1 — `SpawnConfig.OverlayDir` + `AssemblePrompt` update

**File:** `internal/expert/expert.go`

Add `OverlayDir string` to `SpawnConfig`.

Update `AssemblePrompt`: after the "## Current State" section (user-level), if `OverlayDir` is set, read `{OverlayDir}/state.md` and emit "## Project State" section. Missing overlay file is silently skipped.

**Tests** (`internal/expert/expert_test.go`):
- Both user-level and overlay state present → two sections
- Overlay only (no user state.md) → just project state section
- OverlayDir set but no state.md file → graceful skip
- OverlayDir empty string → no overlay section (backward compat)

### 2.2 — Daemon shared expert spawn wiring

**File:** `internal/daemon/daemon.go`

Update `processInboxMessage` (~line 559-595):

```go
var expertDir, overlayDir, logDir string
if d.isSharedExpert(expertName) {
    userDir, err := mail.ResolveSharedExpertDir(expertName)
    // ... error handling
    expertDir = userDir
    overlayDir = filepath.Join(d.poolDir, "shared-state", expertName)
    logDir = mail.ResolveSharedLogDir(d.poolDir, expertName)
} else {
    expertDir = d.resolveExpertDir(expertName)
    logDir = expertDir
}
```

Set `OverlayDir` on `SpawnConfig`. Write logs to `logDir` instead of `expertDir` for shared experts.

Update MCP config creation to use `WriteTempConfigShared` for shared experts.

**Tests** (`internal/daemon/daemon_test.go`):
- `TestSharedExpert_SpawnConfig` — verify ExpertDir is user-level, OverlayDir is pool-scoped
- `TestSharedExpert_LogsInPoolScope` — logs written to shared-state/{name}/logs/

### 2.3 — Watcher + inbox routing for shared experts

**File:** `internal/daemon/daemon.go`

Three changes in `Run()` (~line 156-162):
1. Watch shared expert inboxes alongside pool-scoped ones
2. Update `resolveExpertName` to match shared inbox paths
3. Update `drainAllInboxes` to drain shared expert inboxes
4. Update `drainInbox` to resolve shared inbox paths

Also update `handleCancel` (line 369) to resolve shared inbox paths.

Also update `Route` in `internal/mail/router.go` — add `sharedNames map[string]bool` parameter so it routes to `ResolveSharedInbox` for shared experts. The daemon passes `d.sharedNamesMap()`.

**Call sites for `mail.Route`:** Only `daemon.handlePostoffice` (daemon.go:259).

**Call sites for `mail.ResolveInbox`:**
- daemon.go:145 (architect inbox watch) — unchanged
- daemon.go:158 (expert inbox watch) — unchanged, shared handled separately
- daemon.go:369 (handleCancel) — needs shared check
- daemon.go:834 (drainInbox) — needs shared check
- daemon.go:1109-1115 (resolveExpertName) — needs shared check

**Tests** (`internal/daemon/daemon_test.go`):
- `TestSharedExpert_InboxDrainedOnStartup` — pre-existing message in shared inbox processed
- `TestSharedExpert_RouteToSharedInbox` — message addressed to shared expert lands in shared-state inbox
- `TestSharedAndPoolScoped_Coexist` — both types work in same pool

---

## Phase 3: Scoped State Writes

### 3.1 — `ServerConfig.IsShared` + `--shared` CLI flag

**File:** `internal/mcp/server.go` — add `IsShared bool` and `SharedOverlayDir string` to `ServerConfig`.

**File:** `internal/mcp/config.go` — add `WriteTempConfigShared(poolDir, expertName string)` that passes `--shared true` in MCP args.

**File:** `cmd/agent-pool/main.go` — update `cmdMCP` to parse `--shared` flag, set `ServerConfig.IsShared` and compute `SharedOverlayDir`.

### 3.2 — Scope-aware `update_state` and `read_state`

**File:** `internal/mcp/tools.go`

Update `RegisterExpertTools` to resolve expertDir via shared-aware helper and pass `cfg` to handlers.

`update_state` gains `scope` parameter (optional, default `"project"`):
- Pool-scoped expert: `scope` ignored, writes to `expertDir/state.md` as today
- Shared expert + `scope=project`: writes to `{SharedOverlayDir}/state.md`
- Shared expert + `scope=user`: writes to `{expertDir}/state.md` (user-level)

`read_state` for shared experts: returns `project_state` field alongside existing `state` field.

`append_error`: unchanged — `expertDir` is already user-level for shared experts.

**Tests** (`internal/mcp/tools_test.go`):
- Shared expert: project scope write, user scope write, default scope
- Pool-scoped: scope parameter ignored
- read_state with project_state for shared expert

### 3.3 — Update flush hook + concierge tools

**File:** `internal/hooks/flush.go` (line 45) — needs to resolve shared expert dir correctly. Add `IsShared bool` to `FlushConfig`, use `mail.ResolveSharedExpertDir` when true.

**File:** `cmd/agent-pool/main.go` — update `cmdFlush` to parse `--shared`.

**File:** `internal/mcp/concierge_tools.go` (line 358) — `readExpertResult` reads logs from expertDir. For shared experts, logs are in `{poolDir}/shared-state/{name}/logs/`. Need to check shared.include list. Since concierge has pool config access, pass shared names through.

---

## Phase 4: Startup Validation

At the end of `ensureDirs`, warn (don't error) if a shared expert's user-level directory is missing `identity.md`:

```go
for _, name := range d.cfg.Shared.Include {
    userDir, _ := mail.ResolveSharedExpertDir(name)
    if _, err := os.Stat(filepath.Join(userDir, "identity.md")); os.IsNotExist(err) {
        d.logger.Warn("Shared expert missing identity.md", "expert", name, "path", userDir)
    }
}
```

---

## Commit Sequence

| # | Scope | Files | Risk |
|---|-------|-------|------|
| 1 | `config.SharedExpertDir` | config.go, config_test.go | None — pure addition |
| 2 | `mail.ResolveShared*` functions | router.go, router_test.go | None — pure addition |
| 3 | Config validation | config.go, config_test.go | Low — existing valid configs unchanged |
| 4 | `ensureDirs` + `isSharedExpert` | daemon.go, daemon_test.go | Low — new dirs only |
| 5 | `SpawnConfig.OverlayDir` + `AssemblePrompt` | expert.go, expert_test.go | Low — empty OverlayDir is no-op |
| 6 | Daemon shared expert spawn | daemon.go, daemon_test.go | Medium — spawn path diverges |
| 7 | Watcher + Route + inbox routing | daemon.go, router.go, *_test.go | Medium — Route signature changes |
| 8 | `ServerConfig.IsShared` + `--shared` + `WriteTempConfigShared` | server.go, config.go, main.go | Low — additive |
| 9 | Scope-aware MCP tools | tools.go, tools_test.go | Medium — handler logic branches |
| 10 | Flush hook + concierge tools | flush.go, concierge_tools.go, main.go | Low — path resolution |
| 11 | Startup validation | daemon.go | None — warn only |

---

## Verification

1. `make test` — full suite passes after each commit
2. **Manual integration test:**
   - Create `~/.agent-pool/experts/test-shared/identity.md` with content
   - Create pool with `shared.include = ["test-shared"]` and a pool-scoped expert
   - Send task to shared expert → verify prompt includes identity from user-level
   - Call `update_state` with scope=project → verify file at `shared-state/test-shared/state.md`
   - Call `update_state` with scope=user → verify file at `~/.agent-pool/experts/test-shared/state.md`
   - Send task to pool-scoped expert → verify unchanged behavior
3. `make test-cover` — new code has tests

## Critical Files

- `internal/config/config.go` — SharedExpertDir, Validate
- `internal/mail/router.go` — ResolveShared*, Route signature
- `internal/expert/expert.go` — SpawnConfig.OverlayDir, AssemblePrompt
- `internal/daemon/daemon.go` — processInboxMessage, ensureDirs, resolveExpertName, drainInbox, handleCancel, Run
- `internal/mcp/tools.go` — RegisterExpertTools, handleUpdateState, handleReadState
- `internal/mcp/config.go` — WriteTempConfigShared
- `internal/mcp/server.go` — ServerConfig.IsShared
- `internal/hooks/flush.go` — FlushConfig.IsShared
- `internal/mcp/concierge_tools.go` — readExpertResult
- `cmd/agent-pool/main.go` — cmdMCP, cmdFlush
