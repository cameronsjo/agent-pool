# v0.9 — Formulas + Polish

## Context

All four roles are wired (concierge v0.5, architect v0.4, expert v0.2, researcher v0.8). The architect currently sends tasks one at a time via `send_task`. Recurring multi-step patterns (feature implementation, bug triage, code review) require manually re-creating the same dependency graph each time. Formulas codify these patterns as reusable TOML templates that the architect instantiates in one call, bulk-creating tasks with correct dependency edges. The daemon's existing taskboard + `EvaluateDeps` handles the rest.

Additionally: config changes currently require daemon restart, and there's no hardening around partial writes for config files.

## Phase 1: Formula Parsing — `internal/formula/`

New package. Minimal structs, TOML parsing, DAG validation.

### Files

- **`internal/formula/formula.go`** — `Formula`, `Step` structs + `Load(path)` + `LoadAll(dir)` + `Validate()`
- **`internal/formula/formula_test.go`** — happy path, cycle detection, duplicate IDs, missing depends_on refs, empty steps

### Design

```go
type Step struct {
    ID          string   `toml:"id"`
    Role        string   `toml:"role"`        // "concierge", "architect", or expert name
    Title       string   `toml:"title"`
    Description string   `toml:"description"`
    DependsOn   []string `toml:"depends_on"`
}

type Formula struct {
    Description string `toml:"description"`
    Steps       []Step `toml:"steps"`
}
```

- `Load(path string) (*Formula, error)` — parse single TOML file, call `Validate()`
- `LoadAll(dir string) (map[string]*Formula, error)` — scan `*.toml`, key by filename sans extension
- `Validate(f *Formula) error` — checks:
  - At least one step
  - No duplicate step IDs
  - All `depends_on` refs point to valid step IDs within the formula
  - No cycles (Kahn's algorithm, same approach as `taskboard.DetectCycles`)
  - Non-empty `id`, `role`, `title` on each step

Uses `github.com/BurntSushi/toml` (already a dependency via `config`).

## Phase 2: Formula Instantiation — architect MCP tool

New `instantiate_formula` tool registered in `RegisterArchitectTools`.

### Files

- **`internal/mcp/architect_tools.go`** — add `instantiate_formula` tool registration + handler
- **`internal/mcp/architect_tools_test.go`** — test instantiation produces correct messages

### Tool Schema

```
instantiate_formula
  formula:    string (required) — formula name (filename without .toml)
  prefix:     string (required) — ID prefix for generated tasks (e.g., "feat-auth")
  overrides:  string (optional) — JSON object: step ID → custom body text
  experts:    string (optional) — JSON object: step ID → specific expert name
```

### Handler Logic

1. Load formula from `{poolDir}/formulas/{formula}.toml` via `formula.Load()`
2. Parse `overrides` and `experts` JSON if provided
3. For each step, build a `mail.Message`:
   - `ID` = `{prefix}-{step.id}`
   - `From` = `"architect"`
   - `To` = step's role, overridden by `experts[step.id]` if present
   - `Type` = `mail.TypeTask`
   - `Body` = `overrides[step.id]` if present, else step's description (prepend step title as heading)
   - `DependsOn` = step's `depends_on` with prefix applied (e.g., `["gather"]` → `["feat-auth-gather"]`)
   - `Priority` = `mail.PriorityNormal`
   - `Timestamp` = `time.Now().UTC()`
4. Post all messages via `postMessage(poolDir, msg)`
5. Return summary: formula name, N tasks created, task IDs

### Validation

- Formula must exist
- Steps with `role = "experts"` MUST have an entry in `experts` map (architect provides the name)
- Prefix must be filename-safe (same check as message ID: `filepath.Base(prefix) == prefix`)

## Phase 3: Config Hot-Reload

Watch `pool.toml` for changes, re-parse, validate, swap config under lock.

### Files

- **`internal/daemon/daemon.go`** — add `reloadConfig()` method, handle pool.toml events in `Run()`
- **`internal/daemon/watcher.go`** — extend `Run()` to also emit events for `Write` on `.toml` files (not just `Create` on `.md`)
- **`internal/daemon/daemon_test.go`** — test: modify pool.toml while daemon runs, verify new expert becomes spawnable

### Watcher Changes

Current watcher only handles `Create` events on `.md` files. For config reload:

- Add a new event type flag or use `WatcherEvent.Dir` to distinguish config events
- In `Run()`, also match `fsnotify.Write` events on files ending in `.toml`
- Apply `waitForStable` before emitting
- Pool directory is already watched (for postoffice). But `pool.toml` lives at `{poolDir}/pool.toml` — the watcher watches `{poolDir}/postoffice/`, not `{poolDir}/` itself. **Must add `poolDir` itself to the watcher.**

### Daemon Changes

In `Run()`, detect pool.toml events (check `event.Path` ends with `pool.toml`):

```go
func (d *Daemon) reloadConfig() error {
    newCfg, err := config.LoadPool(d.poolDir)
    if err != nil {
        d.logger.Warn("Config reload failed, keeping current config", "error", err)
        return err  // keep old config
    }
    d.mu.Lock()
    defer d.mu.Unlock()
    oldCfg := d.cfg
    d.cfg = newCfg
    // Rebuild shared expert lookup set
    d.sharedSet = make(map[string]bool, len(newCfg.Shared.Include))
    for _, name := range newCfg.Shared.Include {
        d.sharedSet[name] = true
    }
    // Diff and log changes
    d.logConfigDiff(oldCfg, newCfg)
    return nil
}
```

For expert add/remove: after reload, call `ensureDirs()` and add new inbox dirs to the watcher. For removals, stop watching but preserve directories.

New event type: `EventConfigReloaded`.

## Phase 4: Hardening

- **`internal/daemon/daemon.go`** — add `formulas/` to `ensureDirs()`
- **Audit atomic writes** — verify all daemon-consumed file paths use `atomicfile.WriteFile` (taskboard already does, postoffice already does, verify nothing was missed)
- **TOML validation in reload** — already handled by `config.LoadPool` which calls `Validate()`. Parse failure = log warning + keep old config (built into Phase 3)

## Implementation Order

1. Phase 1 (formula parsing) — standalone, no dependencies
2. Phase 2 (instantiation tool) — depends on Phase 1
3. Phase 3 (config hot-reload) — independent of 1+2
4. Phase 4 (hardening) — after all above, quick sweep

Phases 1 and 3 can be developed in parallel, but I'll do them sequentially since 1 is a prerequisite for 2 and I want to test the full flow.

## Key Patterns to Follow

- **Test file structure**: comment block at top documenting coverage matrix (see existing `*_test.go` files)
- **Chicago-school tests**: real objects, fakes only at I/O boundaries
- **Error wrapping**: `fmt.Errorf("context: %w", err)`
- **Atomic writes**: `atomicfile.WriteFile` for all daemon-consumed files
- **Reuse `postMessage`** (`internal/mcp/postoffice.go:15`) for formula instantiation
- **Reuse `splitCSV`** (`internal/mcp/architect_tools.go:301`) for parsing parameters
- **MCP tool registration** follows `RegisterArchitectTools` pattern (`:23`)
- **Event bus** emits at slog log points — add events for new behaviors

## Verification

After each phase:
1. `make test` — all tests pass
2. `make check` — vet + lint + test

End-to-end verification:
1. Create a test formula TOML, load and validate it
2. Instantiate via the MCP tool, verify N messages posted with correct dependency edges
3. Modify pool.toml in a daemon integration test, verify config swap + new expert watcher
4. `make test-cover` — check coverage for new packages
