# v0.9 — Formulas + Polish

## Where We Are

v0.8 is merged on main. The researcher role is fully operational: daemon
watches researcher inbox, spawns sessions with role-specific MCP tools,
and a curation scheduler auto-triggers after configurable task or time
thresholds. Six researcher tools (list_experts, read_expert_state,
read_expert_logs, enrich_state, write_expert_state, promote_pattern)
enable cross-expert state curation with shared-expert-aware layered state.
Cold-start seeding via `agent-pool seed` bootstraps expert state through
the researcher. Log rotation archives old logs into tar.gz bundles.

All four roles are now wired: concierge (v0.5), architect (v0.4),
expert (v0.2), researcher (v0.8). The pool is a complete system.

## What v0.9 Adds

Per docs/plans/architecture.md § Implementation Phasing:

▎ Workflow templates and operational hardening.

Scope:
- TOML formula parsing (`{poolDir}/formulas/*.toml`)
- Formula instantiation by architect (new MCP tool)
- Config hot-reload (watch pool.toml for changes, apply without restart)
- Partial-write detection hardening on mail files

Validates: Can common workflows be templated and reused across pools?

## Key Design Context

### Workflow Formulas

Today the architect manually decomposes work: define contracts, send
tasks with `depends_on` edges, verify results. This works but is
repetitive for recurring patterns (feature implementation, bug triage,
code review). Formulas codify these patterns as reusable TOML templates.

The critical design choice: **the daemon evaluates dependencies and
dispatches — no LLM needed for sequencing.** Formulas are deterministic
DAGs. The architect provides the creative decomposition (what to ask
each expert); the formula provides the structure (which roles in what
order). The architect _instantiates_ a formula, filling in the
task-specific details for each step.

```toml
# formulas/feature-impl.toml
description = "Standard feature implementation flow"

[[steps]]
id = "gather"
role = "concierge"
title = "Gather expert input"
description = "Ask targeted questions to relevant experts"

[[steps]]
id = "plan"
role = "concierge"
title = "Build plan"
description = "Synthesize expert input into plan/spec"
depends_on = ["gather"]

[[steps]]
id = "review"
role = "architect"
title = "Review plan + define contracts"
description = "Review plan, identify boundaries, define contracts"
depends_on = ["plan"]

[[steps]]
id = "implement"
role = "experts"
title = "Implementation"
description = "Experts execute in parallel, building to contracts"
depends_on = ["review"]

[[steps]]
id = "verify"
role = "architect"
title = "Verification"
description = "Verify each expert's output against contracts"
depends_on = ["implement"]
```

When the architect calls `instantiate_formula`, the daemon:
1. Parses the formula TOML
2. Creates a task for each step, with the formula's `depends_on` edges
3. Fills in architect-provided overrides (specific expert names, task bodies)
4. Registers all tasks in the taskboard
5. Dispatches the first ready step (no dependencies)

The taskboard already handles dependency evaluation (`EvaluateDeps`).
Formulas just bulk-register tasks with pre-defined dependency graphs.

### Formula Structure

```
{poolDir}/formulas/
├── feature-impl.toml        # Standard feature flow
├── bug-triage.toml           # Bug investigation + fix
├── code-review.toml          # Review + feedback cycle
└── index.md                  # Auto-generated summary
```

Each formula is a standalone TOML file. The `[[steps]]` array defines
the DAG. Each step has:
- `id` — unique within the formula (used in `depends_on`)
- `role` — "concierge", "architect", or a specific expert name
- `title` — short description (becomes task summary)
- `description` — detailed instructions for the role
- `depends_on` — list of step IDs that must complete first

### Instantiation

The architect calls `instantiate_formula` with:
- `formula` — formula filename (without .toml)
- `prefix` — ID prefix for generated tasks (e.g., "feat-auth" → "feat-auth-gather")
- `overrides` — JSON map of step ID → custom body text
- `experts` — JSON map of step ID → specific expert name (for steps with `role = "experts"`)

This produces N tasks in the postoffice, all with correct dependency edges.

### Config Hot-Reload

Today, changing `pool.toml` requires restarting the daemon. v0.9 adds
an fsnotify watcher on `pool.toml` that:
1. Detects writes to pool.toml
2. Re-parses with `config.LoadPool`
3. Validates the new config
4. Updates `d.cfg` under lock
5. Adjusts watchers if experts were added/removed
6. Logs what changed

This is useful for adding experts to a running pool without downtime.
The daemon already watches directories — extending to watch a single
file is straightforward. The main risk is partial-write detection:
pool.toml could be mid-write when fsnotify fires.

### Partial-Write Detection

The watcher already has `waitForStable` for mail files (polls until
file size stops changing). Two enhancements:
1. Apply `waitForStable` to pool.toml reloads
2. Validate TOML parse succeeds before accepting the new config
3. If parse fails, log a warning and keep the old config (don't crash)

For mail files, the `.routing-*` temp file pattern plus atomic rename
already prevents partial reads. The enhancement here is defensive —
verify that `atomicfile.WriteFile` is used consistently for all
daemon-consumed files, and add a TOML validation step to the reload.

## What Already Exists

**Taskboard (internal/taskboard/):**
- `Board.Add(task)` registers tasks with `DependsOn` edges
- `Board.EvaluateDeps()` returns newly-ready task IDs
- `Board.Save(path)` persists to JSON
- Tasks have `Status`, `Expert`, `DependsOn`, `ID` fields
- The daemon calls `registerTask()` for each postoffice message

**Config (internal/config/config.go):**
- `LoadPool(poolDir)` parses and validates pool.toml
- `PoolConfig` has all role sections, curation section, defaults
- No formula-related fields yet

**Watcher (internal/daemon/watcher.go):**
- `Watcher.Add(dir)` watches a directory for Create events on .md files
- `waitForStable(path)` polls for file size stability
- Filters `.routing-*` temp files
- Currently only watches directories, not individual files

**Architect tools (internal/mcp/architect_tools.go):**
- `send_task` dispatches a single task with contracts + depends_on
- `define_contract` creates a versioned interface spec
- No formula-related tools yet

**Mail (internal/mail/):**
- `Post(poolDir, msg)` composes + atomic write to postoffice
- Messages have `DependsOn []string` field
- Router handles delivery to inboxes

**Gap: No formula parsing.** No TOML formula struct, no loader.

**Gap: No formula instantiation.** Architect can't bulk-create tasks.

**Gap: No file watcher for pool.toml.** Watcher only handles directories.

**Gap: No config reload path.** `d.cfg` is set once in `New()`.

## What to Read First

1. docs/plans/architecture.md — §Workflow Formulas, §Resolved Decisions
2. internal/taskboard/ — Board, Task struct, Add, EvaluateDeps
3. internal/daemon/daemon.go — registerTask, resolveExpertConfig, Run
4. internal/daemon/watcher.go — Watcher, waitForStable, Run
5. internal/mcp/architect_tools.go — handleSendTask (single-task dispatch pattern)
6. internal/config/config.go — LoadPool, PoolConfig struct

## Approach Suggestion

**Phase 1: Formula Parsing**
New package `internal/formula/` with `Formula` and `Step` structs.
`Load(path)` parses a single TOML file. `LoadAll(formulasDir)` scans
the directory. `Validate(formula)` checks for DAG cycles, missing
step IDs in `depends_on`, and duplicate IDs. Add `formula_test.go`
with cycle detection, happy path, and malformed input tests.

**Phase 2: Formula Instantiation**
New architect tool `instantiate_formula`. Takes formula name, prefix,
overrides, and expert assignments. Generates N `mail.Message`s with
correct `DependsOn` edges (prefixed IDs). Posts all to postoffice.
The daemon's existing taskboard + dependency evaluation handles the rest.
This is the highest-value phase — it turns the formula into running tasks.

**Phase 3: Config Hot-Reload**
Add `pool.toml` to the watcher (may need a file-level watch, not just
directory). On change: waitForStable, re-parse, validate, swap under
lock. Diff old vs new config to log changes. Adjust expert inbox
watchers if experts were added or removed. Add a new event type
`config.reloaded`. Test: modify pool.toml while daemon runs, verify
new expert becomes spawnable.

**Phase 4: Hardening**
- Ensure all daemon-consumed file writes use `atomicfile.WriteFile`
- Add TOML validation step to config reload (parse failure = keep old)
- Formula directory auto-created in `ensureDirs`
- Formula index auto-generated (like contracts/index.md)

## Design Questions to Resolve

1. **Formula location:** `{poolDir}/formulas/` (pool-scoped) vs
   `~/.agent-pool/formulas/` (user-scoped, like shared experts)?
   Recommendation: pool-scoped. Formulas reference pool-specific roles
   and experts. Sharing between pools is a copy-paste concern, not a
   runtime concern.

2. **Role = "experts" expansion:** When a step has `role = "experts"`,
   does the architect provide a specific expert name at instantiation,
   or does the daemon resolve it? Recommendation: architect provides
   via the `experts` map. The daemon doesn't know which expert is right
   for a given task — that's the architect's judgment.

3. **Formula versioning:** Should formulas track version numbers like
   contracts? Recommendation: no. Formulas are templates, not agreements
   between parties. Git versioning is sufficient.

4. **Watcher type for pool.toml:** fsnotify.Write event on a specific
   file vs watching the parent directory + filtering for pool.toml?
   Recommendation: watch the pool directory (already watched for
   postoffice) and filter for `pool.toml` by filename. Avoids needing
   a separate watcher for a single file.

5. **Expert add/remove during runtime:** When config reload detects a
   new expert, should the daemon auto-create inbox dirs and start
   watching? Recommendation: yes — call `ensureDirs` again and add
   the new inbox to the watcher. For removals, stop watching but don't
   delete directories (data preservation).
