# v0.5 — Concierge Plugin

## Where We Are

v0.4 is merged on main. The system now has a full verification loop:
contracts, architect spawning with Opus, 4 architect MCP tools
(define_contract, send_task, verify_result, amend_contract), contract
amendment fan-out, and a human approval gate with stdout/file modes.

v0.3 delivered: taskboard.json, DAG dependency evaluation, cancel/handoff
handling, session timeout. v0.2 delivered: MCP server (stdio, per-expert),
6 expert tools, Stop/PreToolUse hooks.

Everything so far is daemon-side — headless processes that a human interacts
with by writing files to the postoffice. There's no user-facing integration.

## What v0.5 Adds

Per docs/plans/architecture.md § Implementation Phasing:

▎ The user-facing integration.

Scope:
- Claude Code plugin with plugin.json and .mcp.json — the concierge runs
  as the user's interactive Claude Code session, not a headless daemon-spawned
  process
- Concierge MCP tools — `pool_ask_expert`, `pool_submit_plan`,
  `pool_check_status` (minimum set for read/write path flows)
- Skills — `pool-ask` (read path), `pool-build` (write path),
  `pool-status` (taskboard query)
- Read path flow — user asks → concierge dispatches questions to experts →
  experts respond → concierge synthesizes into unified answer
- Write path flow — user requests feature → concierge gathers expert input →
  concierge builds plan → architect reviews + defines contracts → experts
  execute → architect verifies → results returned to user

Validates: Does the full read/write path flow feel fluid from the user's
perspective?

## Key Architecture Context

### The Concierge Is Not Daemon-Spawned

The concierge is the user's interactive Claude Code session. It is NOT a
headless session like experts and the architect. It runs inside Claude Code
with the plugin loaded, giving it access to pool tools via MCP.

This is a fundamental difference from all other roles:
- Experts/architect: spawned by daemon via `claude -p`, disposable sessions
- Concierge: the user's live session, persistent, interactive

The concierge never touches the postoffice directly. It uses MCP tools that
handle mail composition and delivery internally.

### Plugin Structure (architecture.md § Concierge Integration)

```
agent-pool-plugin/
├── plugin.json
├── .mcp.json                        # Points at agent-pool binary
├── skills/
│   ├── pool-ask.md                 # Read path: ask experts, synthesize
│   ├── pool-build.md              # Write path: gather → plan → architect
│   └── pool-status.md             # Check task/pool status
└── agents/
    ├── architect.md                # Architect agent definition (future)
    └── researcher.md              # Researcher agent definition (future)
```

The `.mcp.json` points at the agent-pool binary in "concierge" mode:
```json
{
  "mcpServers": {
    "agent-pool": {
      "command": "agent-pool",
      "args": ["mcp", "--pool", "${AGENT_POOL_DIR}", "--role", "concierge"],
      "type": "stdio"
    }
  }
}
```

### Concierge MCP Tools

| Tool | Purpose | Flow |
|------|---------|------|
| `pool_ask_expert` | Send a question to an expert, wait for response | Read path |
| `pool_submit_plan` | Send a plan to the architect for review/decomposition | Write path |
| `pool_check_status` | Query taskboard for in-flight task status | Both paths |
| `pool_list_experts` | Show available experts (pool-scoped + shared) | Discovery |
| `pool_request_research` | Trigger researcher for enrichment | Out of v0.5 scope |

### Read Path Flow (architecture.md)

```
User: "How does feature Y work across frontend, backend, and ETL?"
  → Concierge dispatches questions to FE, BE, Pipeline experts (parallel)
  → Experts respond with domain-specific answers
  → Concierge synthesizes into coherent cross-cutting narrative
  → User gets unified explanation
```

The concierge needs to:
1. Compose question messages (type: question) to relevant experts
2. Write them to the postoffice
3. Wait for response messages to arrive back
4. Read response bodies
5. Synthesize into a single answer

The waiting is the hard part. Options:
- **Poll taskboard** — the tool writes questions to postoffice, then polls
  taskboard until all are completed, then reads response logs
- **Synchronous spawn** — the tool spawns experts inline and waits (bypasses
  daemon entirely, but loses parallelism and taskboard tracking)
- **File watch** — watch for response files in concierge's inbox

Recommended: **poll taskboard.** It reuses the existing infrastructure, the
daemon handles spawning and parallelism, and the concierge just needs to
wait for completion. The `pool_ask_expert` tool would:
1. Compose and write question message to postoffice
2. Poll taskboard until the task reaches completed/failed status
3. Read the expert's response from the task log
4. Return the response body to the concierge session

### Write Path Flow (architecture.md)

```
User: "Build OAuth login flow"
  → Concierge dispatches questions to auth, FE, BE experts (gather input)
  → Concierge synthesizes expert input into plan/spec
  → Concierge submits plan to architect via pool_submit_plan
  → Architect reviews, defines contracts, dispatches tasks
  → Experts execute in parallel (building to contracts)
  → Architect verifies each against contracts
  → Results returned to concierge → user
```

`pool_submit_plan` sends the plan to the architect and returns immediately
(or waits for architect completion, depending on design). The architect's
output includes contracts defined and tasks dispatched. The concierge can
then track progress via `pool_check_status`.

### What Already Exists

**Config (internal/config/config.go):**
- `Concierge RoleSection` with Model and SessionTimeout fields
- `RoleSection` is the common config for concierge/researcher

**Mail (internal/mail/):**
- `TypeQuestion` message type exists, routes to expert inboxes
- `TypeResponse` for expert replies back to sender
- `ResolveInbox` already routes concierge to `{poolDir}/concierge/inbox/`
- `IsBuiltinRole("concierge")` returns true
- `ResolveExpertDir` handles concierge state directory

**Daemon (internal/daemon/daemon.go):**
- `ensureDirs()` already creates `concierge/inbox/`
- Architect inbox watching pattern from v0.4 can be extended to concierge
  (though concierge is NOT daemon-spawned, its inbox is used for responses)
- `registerTask` tracks question-type messages in taskboard
- `markTaskCompleted` evaluates deps and wakes blocked tasks

**MCP (internal/mcp/):**
- `ServerConfig` has Role field, `WriteTempConfigForRole` generates config
- `RegisterArchitectTools` pattern shows how to add role-specific tool sets
- `Run()` conditionally registers tools based on `cfg.Role`

**Taskboard (internal/taskboard/):**
- Tasks track From, Expert, Status, CompletedAt
- Status lifecycle: pending → active → completed/failed
- `board.Tasks` map is queryable for status checks

**Gap: Response retrieval.** When the concierge sends a question, the expert's
response goes to `concierge/inbox/` as a mail file. But there's no mechanism
to read responses from the inbox programmatically. The concierge needs either:
(a) a tool that reads from its own inbox, or (b) read the expert's task log
instead (the output is captured in `{expertDir}/logs/{task-id}.json`).

Option (b) is cleaner — the task log contains the full session output, and
the task ID is known from when the question was submitted.

**Gap: Plugin infrastructure.** No plugin files exist yet. The plugin.json,
.mcp.json, and skill files need to be created from scratch.

**Gap: Concierge inbox watching.** The daemon watches architect and expert
inboxes but not the concierge inbox. Since the concierge is interactive (not
daemon-spawned), it doesn't need inbox watching for spawning — but it might
need it for notification delivery.

## What to Read First

1. docs/plans/architecture.md — §Read Path, §Write Path, §Concierge
   Integration, §Implementation Phasing v0.5
2. internal/mcp/architect_tools.go — tool handler patterns, approval gate
   integration (similar waiting pattern needed for pool_ask_expert)
3. internal/mcp/server.go — role-aware tool registration
4. internal/taskboard/taskboard.go — Board/Task types, status queries
5. internal/daemon/daemon.go — handlePostoffice dispatch, task lifecycle,
   response routing
6. internal/expert/expert.go — log reading (ReadLog, SearchIndex)
7. Cameron's plugin workbench at ~/Projects/claude-configurations/ for
   plugin.json and .mcp.json format reference

## Approach Suggestion

This has two distinct parts: the MCP tools (Go code, daemon integration)
and the plugin (plugin.json, skills, no Go). Plan-first recommended.

**Phase 1: Concierge MCP Tools**
Add `RegisterConciergeTools()` in internal/mcp/. Three tools minimum:
- `pool_ask_expert` — compose question, write to postoffice, poll taskboard,
  return response from expert log
- `pool_submit_plan` — compose task message to architect, write to postoffice,
  optionally poll for completion
- `pool_check_status` — read taskboard, return formatted status

Also add `pool_list_experts` for discovery (reads pool.toml experts section).

The concierge also inherits expert tools (read_state, update_state, etc.)
for managing its own state.

**Phase 2: Concierge Spawning Support**
The concierge is NOT daemon-spawned, but `--role concierge` needs to work
for the MCP server. Extend the existing role-aware pattern:
- `cmdMCP()` already accepts `--role` — just needs concierge tool registration
- `ServerConfig` already has Role field
- Pool config loading for concierge (model, session timeout)

**Phase 3: Plugin Scaffold**
Create the plugin directory structure:
- `plugin.json` — plugin metadata, manifest
- `.mcp.json` — points at agent-pool binary with --role concierge
- Skills: `pool-ask.md`, `pool-build.md`, `pool-status.md`

Skills are the user-facing interface — they translate user intent into
tool calls. `pool-ask` guides the read path, `pool-build` guides the
write path, `pool-status` formats taskboard output.

**Phase 4: Read Path Integration Test**
End-to-end: user invokes pool-ask skill → concierge sends questions →
daemon routes to experts → experts respond → concierge reads responses →
synthesized answer returned.

This requires a running daemon with at least one configured expert. Could
be tested with the fakeSpawner pattern from daemon tests.

**Phase 5: Write Path Integration Test**
End-to-end: user invokes pool-build skill → concierge gathers input →
submits plan to architect → architect defines contracts → experts execute →
architect verifies → results returned.

This is the full loop. More complex — involves architect approval gate,
contract creation, and multi-expert coordination.

## Design Questions to Resolve

1. **How does pool_ask_expert wait for responses?** Poll taskboard (recommended)
   vs. watch inbox vs. synchronous spawn. Polling interval and timeout.

2. **Should pool_submit_plan block until the architect finishes?** If yes,
   it could take minutes (architect reviews, dispatches, experts execute,
   architect verifies). If no, the user uses pool_check_status to track.
   Recommendation: return immediately with task ID, user tracks via status.

3. **Where does the plugin live?** Options: (a) in this repo under
   `plugin/`, (b) in a separate repo, (c) in Cameron's workbench at
   ~/Projects/claude-configurations/. The architecture doc shows it in the
   plugin directory. For v0.5, keep it in this repo for co-development.

4. **How does the concierge know which experts to ask?** The read path
   requires the concierge to decide which experts are relevant. Options:
   (a) user specifies experts explicitly, (b) concierge uses
   pool_list_experts and picks based on the question, (c) concierge asks
   all experts (expensive). Recommendation: (a) for v0.5, skill prompts
   guide the user to specify experts.

5. **Does the concierge need its own state files?** identity.md for the
   concierge role, state.md for tracking in-flight work across sessions.
   Recommendation: yes, identity.md at minimum. The concierge's identity
   defines how it interacts with the user and delegates to experts.
