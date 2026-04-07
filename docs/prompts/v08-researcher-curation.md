# v0.8 — Researcher + Curation

## Where We Are

v0.7 is merged on main. Shared experts work across pools: user-level
identity in `~/.agent-pool/experts/`, per-pool project overlays in
`shared-state/`, layered prompt assembly, scope-aware `update_state`.
Config parsing, mail routing, and directory creation for the researcher
role were scaffolded in prior versions but nothing watched the inbox
or provided tools.

## What v0.8 Adds

Per docs/plans/architecture.md § Implementation Phasing:

▎ Knowledge enrichment and hygiene via a dedicated researcher role.

Scope:
- Researcher role wired into daemon lifecycle (inbox watch, spawn, config)
- Six researcher MCP tools for cross-expert state management
- Curation scheduling (task count + time interval triggers)
- Cold-start seeding via `agent-pool seed` CLI command
- Log rotation (tar.gz archival beyond retention threshold)
- Cross-pool shared expert enrichment with layered state awareness

Validates: Does curation keep state.md lean over time? Does cold-start
seeding produce useful initial state?

## Key Design Context

### The Curation Model

The researcher is the pool's knowledge curator. It reads expert state
and logs, reasons about what to keep/prune/promote, and writes curated
results back. The two-step pattern (enrich_state reads everything,
write_expert_state writes back) keeps the LLM in control — tools
provide the data plane, not the decision logic.

### Researcher Tools

| Tool | R/W | Purpose |
|------|-----|---------|
| list_experts | R | Triage: state sizes, log counts, last task |
| read_expert_state | R | Read another expert's identity/state/errors |
| read_expert_logs | R | Recent log index entries with query filter |
| enrich_state | R | Full context assembly for curation analysis |
| write_expert_state | W | Write curated state back to any expert |
| promote_pattern | W | Graduate patterns from state to identity |

### Curation Scheduling

The daemon tracks task completions. After `interval_tasks` (default 10)
or `interval_hours` (default 168h), it generates a structured curation
task describing which experts to curate, their state sizes, and
instructions. Log rotation runs before each curation trigger.

### Pattern Promotion

`promote_pattern` moves knowledge from state.md (working memory) to
identity.md (permanent knowledge). This is the key semantic transition:
patterns that recur across tasks graduate from ephemeral state to
durable identity. The researcher decides what crosses this boundary.
