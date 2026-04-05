# v0.7 — Shared Experts + Multi-Pool

## Where We Are

v0.6 is merged on main. The daemon now has full lifecycle management: Unix
domain socket for CLI→daemon communication, graceful drain with WaitGroup,
`stop`/`status`/`watch` commands, event bus with NDJSON streaming, session
timeout made optional, and `pool_` prefix dropped from all MCP tools.

Two post-merge fixes landed on main after v0.6:
- Signal race in double-signal handler (replaced NotifyContext with buffered channel)
- Watch command Ctrl-C suppression (atomic.Bool flag prevents "stream interrupted" error)

Open issues (#14, #15, #16) from dogfooding are minor and not blocking v0.7.

## What v0.7 Adds

Per docs/plans/architecture.md § Implementation Phasing:

▎ Cross-project knowledge via layered expert state.

Scope:
- `~/.agent-pool/experts/` shared directory for user-level specialists
- `shared.include` in pool.toml registers shared experts to a pool
- Layered state assembly: user-level identity + state, overlaid with per-pool project state
- `scope` parameter in `update_state` MCP tool ("user" vs "project")
- Per-pool `shared-state/{name}/` overlay directories auto-created on first use
- Multi-pool daemon (watch multiple pools from one process)

Validates: Does the shared expert model work across pools? Does layered
state assembly produce coherent prompts?

## Key Design Context

### The Shared Expert Model

Today, experts are pool-scoped. A `security-standards` expert bootstrapped
in one pool can't be reused in another without duplicating its identity and
accumulated state. v0.7 lifts experts to user scope:

```
~/.agent-pool/experts/{name}/
├── identity.md     # What this expert knows (quasi-static)
├── state.md        # Cross-project working memory
├── errors.md       # Append-only failure log
└── logs/
    └── index.md
```

Pools reference shared experts via `shared.include`:

```toml
[shared]
include = ["corporate-policies", "security-standards"]
```

### Layered State Assembly

When a pool invokes a shared expert, the daemon assembles the prompt from
two state layers:

```
identity.md          ← ~/.agent-pool/experts/{name}/identity.md
state.md (user)      ← ~/.agent-pool/experts/{name}/state.md
state.md (project)   ← {poolDir}/shared-state/{name}/state.md
errors.md            ← ~/.agent-pool/experts/{name}/errors.md
task                 ← from the pool's mail system
```

The expert doesn't know it's shared. It sees one coherent prompt. The
daemon handles the layering.

Pool-scoped experts continue to work exactly as today (no user layer).

### State Write Routing

The `update_state` MCP tool gains a `scope` parameter:
- `scope: "project"` (default) → writes to `{poolDir}/shared-state/{name}/state.md`
- `scope: "user"` → writes to `~/.agent-pool/experts/{name}/state.md`

For pool-scoped experts, `scope` is ignored — writes always go to `{expertDir}/state.md`.

### Multi-Pool Daemon

Today the daemon watches one pool directory. v0.7 lets one daemon process
manage multiple pools:

```bash
agent-pool start ~/.agent-pool/pools/api-gateway ~/.agent-pool/pools/data-platform
```

Or discover all pools:

```bash
agent-pool start --all
```

Each pool gets its own watcher, taskboard, and socket. Shared experts are
resolved from the global `~/.agent-pool/experts/` directory.

## What Already Exists

**Expert state files (internal/expert/state.go):**
- `ReadState(expertDir)` reads identity.md, state.md, errors.md
- `WriteState(expertDir, content)` validates + atomic write
- `MaxStateSize = 50_000` bytes

**Prompt assembly (internal/expert/expert.go):**
- `AssemblePrompt(cfg *SpawnConfig)` reads state files + builds prompt
- Sections: identity, state, errors, task (4 sections)
- `SpawnConfig.ExpertDir` is the single state directory
- v0.7 needs to extend this to read from two directories for shared experts

**Config parsing (internal/config/config.go):**
- `SharedSection` struct exists: `Include []string \`toml:"include"\``
- `LoadPool()` already parses `[shared]` section
- But nothing in the daemon uses `Shared.Include` yet

**MCP tools (internal/mcp/tools.go):**
- `update_state` handler writes to `{expertDir}/state.md`
- No `scope` parameter currently
- `ExpertToolNames` in config.go lists tools for `--allowedTools`

**Daemon (internal/daemon/daemon.go):**
- `processInboxMessage` resolves expert dir via `mail.ResolveExpertDir`
- `resolveExpertConfig` gets model/tools for an expert name
- Pool-scoped experts: `{poolDir}/experts/{name}/`
- Built-in roles: `{poolDir}/{role}/`
- No shared expert resolution path exists yet

**Mail routing (internal/mail/):**
- `ResolveExpertDir(poolDir, name)` handles pool-scoped and built-in roles
- `ResolveInbox(poolDir, name)` routes to expert inbox directories
- Shared experts would need new resolution logic

**Gap: No shared expert directory.** The daemon doesn't look at
`~/.agent-pool/experts/`. No code resolves shared expert paths.

**Gap: No layered state assembly.** `AssemblePrompt` reads from one directory.
Shared experts need two (user + project overlay).

**Gap: No `scope` in update_state.** Writes go to one location.

**Gap: Single-pool daemon.** `Run()` accepts one poolDir. Multi-pool would
need multiple watcher+taskboard instances.

## What to Read First

1. docs/plans/architecture.md — §Shared Expert Assembly, §Directory Structure
2. internal/expert/expert.go — AssemblePrompt, SpawnConfig struct
3. internal/expert/state.go — ReadState, WriteState
4. internal/daemon/daemon.go — processInboxMessage, resolveExpertConfig, resolveExpertDir
5. internal/config/config.go — SharedSection, PoolConfig struct, LoadPool
6. internal/mail/router.go — ResolveExpertDir, ResolveInbox, IsBuiltinRole

## Approach Suggestion

**Phase 1: Shared Expert Resolution**
Add `config.ResolveSharedExpertDir(name)` that returns `~/.agent-pool/experts/{name}/`.
Update `mail.ResolveExpertDir` and `mail.ResolveInbox` to handle shared experts
(check `shared.include` list before falling back to pool-scoped). Update
`daemon.ensureDirs` to create shared-state overlay dirs. Add tests.

**Phase 2: Layered Prompt Assembly**
Extend `SpawnConfig` with optional `OverlayDir string` for the project-level
state. Update `AssemblePrompt` to read state.md from both `ExpertDir` and
`OverlayDir` when set, concatenating them under a "## Project State" heading.
Update `processInboxMessage` to set `OverlayDir` for shared experts. Add tests
for the layered assembly (both layers present, project-only, user-only, neither).

**Phase 3: Scoped State Writes**
Add `scope` parameter to `update_state` MCP tool. Route writes to the correct
directory based on scope and whether the expert is shared. Update `WriteState`
or add a new `WriteProjectState` function. Requires the MCP server to know
whether the current expert is shared (pass via `ServerConfig`). Add tests.

**Phase 4: Multi-Pool Daemon**
Refactor `daemon.Run()` to accept multiple pool dirs. Each pool gets its own
goroutine with watcher + taskboard. Shared expert directories are watched
once globally. Socket per pool (or single socket with pool-scoped methods).
This is the riskiest phase — consider whether to do it now or defer.

**Phase 5: Wiring + Integration**
Update `cmdStart` to accept multiple pool dirs or `--all` flag. Update
`cmdStop`/`cmdStatus`/`cmdWatch` to work with multi-pool. Update plugin
skills and bosun rules if needed.

## Design Questions to Resolve

1. **Shared expert inbox location:** `~/.agent-pool/experts/{name}/inbox/`
   (user-level) vs `{poolDir}/shared-state/{name}/inbox/` (pool-scoped)?
   If user-level, messages from different pools could interleave. If
   pool-scoped, the daemon needs to route to different inboxes per pool.
   Recommendation: pool-scoped inbox (`{poolDir}/shared-state/{name}/inbox/`)
   — the expert's identity is shared, but the work queue is per-pool.

2. **Shared expert logs:** Same question. Logs contain pool-specific
   task results. Recommendation: pool-scoped logs under shared-state/.

3. **Multi-pool socket:** One socket per pool (like today, just multiple
   instances) vs one global socket with pool-scoped RPCs?
   Recommendation: one socket per pool — simpler, reuses existing code,
   and `status`/`watch` naturally scope to one pool.

4. **Multi-pool process vs multi-process:** One daemon managing N pools
   vs N separate daemon processes? Recommendation: separate processes
   for v0.7 (just run `agent-pool start` per pool). True multi-pool
   daemon is v0.8+ scope.

5. **Shared expert errors.md:** User-level only (cross-project error log)
   or also per-pool overlay? Recommendation: user-level only — errors are
   about the expert's knowledge gaps, not project-specific state.
