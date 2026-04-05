# v0.4 — Architect + Contracts

## Where We Are

v0.3 is merged on main (or about to be — check PR #3 status). The daemon
now has durable task tracking via taskboard.json, DAG dependency evaluation,
cancel/handoff/timeout handling, and process supervision. Experts have MCP
tools for state management and can send responses through the postoffice.

v0.2 delivered: MCP server (stdio, per-expert), 6 expert tools, Stop/PreToolUse
hooks. v0.3 delivered: taskboard.json, dependency DAG, cancel handling, session
timeout with SIGTERM, handoff tracking with escalation.

## What v0.4 Adds

Per docs/plans/architecture.md § Implementation Phasing:

▎ The verification loop.

Scope:
- Architect role with identity.md — the architect is a headless Claude session
  (like experts) but with Opus model and additional MCP tools for contract
  management and task delegation
- Contract format and storage — markdown files with YAML frontmatter in
  `{poolDir}/contracts/`, indexed by `contracts/index.md`
- Architect MCP tools — `pool_define_contract`, `pool_send_task`,
  `pool_verify_result`, `pool_amend_contract`
- Contract notification fan-out — when a contract is amended (version
  increment), the daemon notifies all parties listed in the `between` field
- Human approval gate — configurable via `approval_mode` in pool.toml:
  "none" (autonomous), "decomposition" (default — human approves plan before
  dispatch), "all" (every dispatch needs approval)

Validates: Does the architect reliably define contracts and verify expert
output? Does the approval gate feel natural?

## Key Architecture Context

### Contract Format (architecture.md § Contracts)

```markdown
---
id: contract-007
type: contract
defined-by: architect
between: [auth, frontend]
version: 1
timestamp: 2026-04-01T14:32:00Z
---

## Auth ↔ Frontend: Token Exchange

### POST /api/auth/token
Request: { ... }
Response (200): { ... }

### Constraints
- accessToken is a JWT, max 15min TTL
- Frontend MUST NOT decode the JWT
```

### Contract Lifecycle

1. Architect defines contracts based on concierge's plan
2. Experts receive contracts as part of task (non-negotiable during execution)
3. Experts flag issues in response (don't deviate silently)
4. Architect verifies output against contract spec
5. Architect amends if needed (version bump, daemon fans out notifications)

### What Already Exists

**Config (internal/config/config.go):**
- `ArchitectSection` already has `Model`, `SessionTimeout`, `ApprovalMode`,
  `HumanInbox` fields with defaults ("decomposition", "stdout")

**Mail (internal/mail/mail.go):**
- `Message.Contracts []string` field already exists, parsed from YAML
- `expert.AssemblePrompt` already includes contracts in task metadata
- `mail.Compose` already serializes the Contracts field

**Daemon (internal/daemon/daemon.go):**
- `ensureDirs()` already creates `architect/inbox/`
- `mail.ResolveInbox` routes to `{poolDir}/architect/inbox/` for built-in roles
- The busy/draining mechanism works for any role name
- Message type dispatch pattern in `handlePostoffice()` is extensible

**MCP (internal/mcp/):**
- `RegisterExpertTools()` pattern in tools.go shows how to add tool sets
- `server.go` calls registration functions and starts stdio server
- Tool handlers use closure pattern for dependency injection

**Gap: Architect spawning.** `resolveExpertConfig()` in daemon.go only checks
`d.cfg.Experts[name]`. The architect config lives in `d.cfg.Architect`, not
in the experts map. The daemon needs to recognize the architect as a special
case in config resolution and spawning.

### Architect MCP Tools (architecture.md § Expert Tool Set)

| Tool | Purpose |
|------|---------|
| `pool_define_contract` | Write a contract to the contracts directory |
| `pool_send_task` | Delegate a task to an expert with contract references |
| `pool_verify_result` | Log verification outcome against a contract |
| `pool_amend_contract` | Update a contract (version bump, notify parties) |

The architect also gets ALL expert tools (read_state, update_state, etc.)
plus these four additional ones.

### Human Approval Gate

```toml
[architect]
approval_mode = "decomposition"    # "none" | "decomposition" | "all"
human_inbox = "stdout"             # "stdout" | "telegram" | "file:~/reviews/"
```

- `none` — Fully autonomous
- `decomposition` — Architect proposes plan, human approves before dispatch
- `all` — Every task dispatch requires human approval

The approval gate is the hardest design problem. "stdout" means the daemon
writes to stdout and blocks until stdin confirms. "file:~/reviews/" means
write to a file and watch for an approval file to appear. "telegram" is
out of scope for v0.4 — focus on stdout and file modes.

## What to Read First

1. docs/plans/architecture.md — §Contracts, §Expert Tool Set (architect
   tools), §Human Approval Gate, §Implementation Phasing v0.4
2. internal/mcp/tools.go — RegisterExpertTools pattern, handler closures
3. internal/mcp/server.go — how tool sets are registered per role
4. internal/daemon/daemon.go — handlePostoffice dispatch, ensureDirs,
   resolveExpertConfig (the gap for architect)
5. internal/config/config.go — ArchitectSection fields
6. internal/expert/expert.go — AssemblePrompt with contracts metadata
7. internal/mail/mail.go — Contracts field, compose.go for serialization

## Approach Suggestion

This is a mix of new subsystem (contracts) and integration work (architect
spawning, approval gate). Plan-first is recommended.

**Phase 1: Contract Package**
New `internal/contract/` package — Contract struct, YAML frontmatter
parse/compose (similar pattern to mail.Parse), storage to contracts/
directory, index.md management, version tracking.

**Phase 2: Architect MCP Tools**
Add `RegisterArchitectTools()` in internal/mcp/tools.go. Four new tools
that read/write contracts and compose task messages. The architect also
inherits all expert tools.

**Phase 3: Architect Spawning**
Wire the daemon to recognize architect messages and spawn with architect
config (Opus model, architect tool set). Fix resolveExpertConfig to handle
built-in roles.

**Phase 4: Contract Amendment Fan-out**
When `pool_amend_contract` is called, the daemon detects the new version
and sends notify messages to all parties in the `between` field.

**Phase 5: Human Approval Gate**
The `pool_send_task` tool checks `approval_mode`. In "decomposition" mode,
it writes a proposal to human_inbox and blocks until approved. Start with
"stdout" mode (simplest). "file" mode can follow.

The approval gate is the riskiest part — it changes the daemon from a
fire-and-forget dispatcher to one that can block on human input. Design
this carefully.
