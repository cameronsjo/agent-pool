# v0.8 — Researcher + Curation

## Context

v0.7 (shared experts + multi-pool) is complete on `feat/v0.7-shared-experts`. The researcher role has been scaffolded across prior versions — config parsing, mail routing, directory creation, built-in role registration — but nothing actually watches its inbox, spawns sessions, or provides tools. v0.8 brings the researcher to life as the pool's knowledge curator: it reads expert state/logs, distills knowledge, promotes patterns to identity, and keeps state.md lean over time.

The architecture doc validates: "Does curation keep state.md lean over time? Does cold-start seeding produce useful initial state?"

## Phase 1: Researcher Daemon Wiring

Wire the researcher into the daemon's event loop so it can receive and execute tasks.

### Files to modify

**`internal/daemon/daemon.go`** — 5 targeted changes:
1. `Run()` (~line 148): Add `watcher.Add()` for researcher inbox, mirroring architect
2. `drainAllInboxes()` (~line 860): Add researcher drain goroutine
3. `resolveExpertName()` (~line 1163): Add researcher inbox → "researcher" mapping
4. `resolveExpertConfig()` (~line 972): Add `d.cfg.Researcher.Model` case
5. `resolveSessionTimeout()` (~line 1126): Add `d.cfg.Researcher.SessionTimeout` case

**`internal/mcp/server.go`** — Replace TODO at line 62:
```go
if cfg.Role == "researcher" {
    RegisterResearcherTools(srv, cfg)
}
```

**`internal/mcp/config.go`** — Add `ResearcherToolNames` slice + `ToolNamesForRole(role string)` helper that returns the correct tool name list per role. Update `ExpertToolNames` comment for clarity.

**`internal/daemon/daemon.go`** — `processInboxMessage` (~line 624): Replace hardcoded `agentmcp.ExpertToolNames` with role-aware `agentmcp.ToolNamesForRole(expertName)`.

### Tests

- `TestResearcherInboxWatched` — drop message in researcher/inbox/, verify spawner called
- `TestResolveExpertConfig_Researcher` — researcher model from pool config
- `TestResolveSessionTimeout_Researcher` — researcher timeout parsing

### Commit
`feat(daemon): wire researcher role into daemon lifecycle`

---

## Phase 2: Researcher MCP Tools

Six tools for cross-expert state management. New file: `internal/mcp/researcher_tools.go`.

| Tool | R/W | Purpose |
|------|-----|---------|
| `list_experts` | R | All experts with state sizes, log counts, last task time |
| `read_expert_state` | R | Read another expert's identity/state/errors files |
| `read_expert_logs` | R | Last N log index entries, optional query filter |
| `enrich_state` | R | Full context assembly for curation (state + recent logs) |
| `write_expert_state` | W | Write curated state back to an expert |
| `promote_pattern` | W | Append graduated pattern to an expert's identity.md |

### Tool details

**`list_experts`** — Loads pool config, stats each expert dir (pool-scoped + shared), returns name/type/state_bytes/log_count/last_task. Richer than concierge's list (which only returns names).

**`read_expert_state`** — Params: `expert` (required), `file` (optional: identity/state/errors/all). Resolves expert dir via `mail.ResolveExpertDir`. Reuses `expert.ReadState()`.

**`read_expert_logs`** — Params: `expert` (required), `count` (optional, default 10), `query` (optional). Reads `logs/index.md`, returns last N entries. Reuses `expert.SearchIndex()` for query.

**`enrich_state`** — Params: `expert` (required). Returns identity + state + errors + last 10 index entries + last 3 full log file contents. This is the "read everything" step before the researcher reasons about curation.

**`write_expert_state`** — Params: `expert` (required), `content` (required), `file` (optional: state/errors, default state). Validates size via `expert.MaxStateSize`. Uses `expert.WriteState()` or `expert.WriteErrors()`.

**`promote_pattern`** — Params: `expert` (required), `pattern` (required), `section` (optional heading, default "## Graduated Patterns"). Reads identity.md, finds/creates section, appends pattern. Atomic write.

### Files

- **Create:** `internal/mcp/researcher_tools.go` (~250 lines)
- **Create:** `internal/mcp/researcher_tools_test.go` (~300 lines)
- **Modify:** `internal/mcp/testhelp_test.go` — add researcher case to `buildMCPTestServer`

### Commit
`feat(mcp): implement researcher tools for cross-expert curation`

---

## Phase 3: Extract `mail.Post()` + Curation Scheduler

### Phase 3a: Extract `mail.Post()`

Move `postMessage` logic from `internal/mcp/postoffice.go` to `internal/mail/post.go` as exported `Post(poolDir string, msg *Message) error`. Thin `mcp.postMessage` becomes `mail.Post(cfg.PoolDir, msg)`. This enables CLI and daemon to post without importing `mcp`.

**Files:**
- **Create:** `internal/mail/post.go`
- **Modify:** `internal/mcp/postoffice.go` — delegate to `mail.Post()`
- **Create:** `internal/mail/post_test.go`

### Phase 3b: Curation Scheduler

New file `internal/daemon/curation.go`:

```go
type curationScheduler struct {
    intervalTasks int
    intervalHours int
    poolDir       string
    logger        *slog.Logger
    taskCount     int
    lastCuration  time.Time
    mu            sync.Mutex
}

func newCurationScheduler(cfg *config.CurationSection, poolDir string, logger *slog.Logger) *curationScheduler
func (cs *curationScheduler) RecordTaskCompletion() bool  // returns true when threshold hit
func (cs *curationScheduler) Reset()
```

**Daemon integration:**
- Add `curation *curationScheduler` field to `Daemon`
- Initialize in `New()`
- In `Run()`: start ticker goroutine for time-based trigger (period = `intervalHours`)
- In `markTaskCompleted()`: call `RecordTaskCompletion()`, trigger curation if true
- New method `triggerCuration(reason string)`: compose curation task message, post via `mail.Post()`

**Event:** Add `EventCurationTriggered` to `internal/daemon/events.go`.

**Curation task body** includes: list of experts, their state sizes, reason for trigger, instructions for the researcher (prune stale state, promote patterns, check sizes).

**Files:**
- **Create:** `internal/daemon/curation.go`
- **Create:** `internal/daemon/curation_test.go`
- **Modify:** `internal/daemon/daemon.go` — integrate scheduler
- **Modify:** `internal/daemon/events.go` — add event type

### Commits
```
refactor(mail): extract Post() for CLI and daemon reuse
feat(daemon): add curation scheduler with task and time triggers
```

---

## Phase 4: Cold-Start Seeding

`agent-pool seed --pool <dir> --expert <name>` — sends a seed task to the researcher.

**`cmd/agent-pool/main.go`:**
- Add `seed` case to command switch
- `cmdSeed()`: parse flags, discover pool dir, validate expert exists, load identity.md for context, compose seed task message (from: "cli", to: "researcher", type: task), call `mail.Post()`, print confirmation
- Update `printUsage()`

**Seed task body:** structured instructions telling the researcher to explore the project codebase (via `project_dir` from pool.toml) and create initial state.md for the named expert based on its identity.md.

### Tests
- `TestCmdSeed_WritesPostoffice` — temp pool, verify message in postoffice
- `TestCmdSeed_UnknownExpert` — verify error

### Commit
`feat(cli): add 'seed' command for cold-start expert bootstrapping`

---

## Phase 5: Log Rotation

New file `internal/expert/rotate.go`:

```go
const DefaultLogRetention = 50

func RotateLogs(expertDir string, retention int) (archived int, err error)
```

Implementation: list `.json` files in `logs/`, sort by mtime newest-first, archive files beyond threshold into `logs/archive-{timestamp}.tar.gz` (stdlib `archive/tar` + `compress/gzip`), delete archived files + matching `.stderr` companions. `index.md` untouched (remains searchable).

**Config:** Add `LogRetention int` to `config.DefaultsSection`, default 50 in `LoadPool()`.

**Integration:** `triggerCuration()` in daemon runs rotation for all experts before generating the researcher task.

### Files
- **Create:** `internal/expert/rotate.go`
- **Create:** `internal/expert/rotate_test.go`
- **Modify:** `internal/config/config.go` — add `LogRetention`
- **Modify:** `internal/daemon/curation.go` — call rotation

### Commit
`feat(expert): add log rotation with configurable retention`

---

## Phase 6: Shared Expert Enrichment

Update Phase 2 tools to handle shared experts with layered state.

**Changes in `researcher_tools.go`:**
- `read_expert_state`: detect shared expert, return both user-level and project overlay state
- `write_expert_state`: add `layer` param ("user"/"project") for shared experts
- `enrich_state`: return both layers clearly labeled
- `promote_pattern`: target user-level identity.md for shared experts (patterns are cross-pool)
- `list_experts`: include shared expert metadata (user + overlay sizes)

Detection: load pool config, check `cfg.Shared.Include`, resolve paths via existing `config.SharedExpertDir()` and `{poolDir}/shared-state/{name}/`.

### Tests
- `TestReadExpertState_SharedExpert` — both layers returned
- `TestWriteExpertState_SharedExpert_UserLayer` / `_ProjectLayer`
- `TestPromotePattern_SharedExpert` — writes user-level identity.md

### Commit
`feat(researcher): enable shared expert enrichment with layered state`

---

## Dependency Graph

```
Phase 1 (daemon wiring) ──┐
                           v
Phase 2 (tools) ──────────┬──> Phase 6 (shared enrichment)
                           │
Phase 3a (mail.Post) ─────┤
                           v
Phase 3b (scheduler) ─────┤
                           v
Phase 4 (seed CLI) ───────┘
                           
Phase 5 (log rotation) ── independent, can parallel with 3-4-6
```

## Deferred

| Item | Why |
|------|-----|
| Auto-seed on empty state.md | Prevents duplicate seeds; manual `seed` command sufficient |
| Per-expert log retention | Global default covers it; trivial to add later |
| Archive extraction in `recall` | index.md still searchable; manual extraction if needed |
| Multi-pool researcher | Requires cross-pool coordination; single-pool is v0.8 scope |
| Curation metrics dashboard | Manual state size checks sufficient for validation |

## Verification

After each phase:
1. `make test` — all existing + new tests pass
2. `make build` — compiles cleanly

End-to-end validation after all phases:
1. Create a test pool with 2 experts and a researcher section in pool.toml
2. Start daemon, send tasks to experts, verify they complete
3. After `interval_tasks` completions, verify curation task appears in researcher/inbox/
4. Spawn researcher manually (`agent-pool mcp --pool <dir> --role researcher`), call `list_experts`, `enrich_state`, `write_expert_state`
5. Run `agent-pool seed --pool <dir> --expert auth`, verify seed message in postoffice
6. Create 60 log files in an expert dir, trigger rotation, verify archive + 50 remaining
