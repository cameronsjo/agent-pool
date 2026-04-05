# First Dogfood: Agent Pool on Bosun

**Date:** 2026-04-05
**Pool:** bosun (4 experts: gitops-engine, docker, daemon-api, config)
**Task:** Triage and fix 6 open GitHub issues (deploy bugs, P1-P2)

## What happened

Built v0.5 (concierge plugin), set up a pool inside the bosun repo, started the daemon, and used the concierge to dispatch investigation and implementation tasks to domain experts. Fixed 5 bugs across 12 files in ~45 minutes.

## The killer feature

Parallel expert dispatch. Three experts investigating different bugs across different packages simultaneously, each returning actionable findings in 1-6 minutes. Sequential would have taken 15-20 minutes. The concierge synthesized results as they came in.

Parallel implementation worked too — 418 lines across 11 files, zero merge conflicts — because each expert owns a distinct package boundary.

## What broke

### Concierge blocks on synchronous tool calls
`pool_ask_expert` polls the taskboard in a blocking loop. Three parallel calls = frozen concierge for the duration. Fixed mid-session by adding `pool_dispatch` (fire-and-forget) + `pool_collect` (one-shot status check). The concierge stays responsive between dispatch and collect.

### Architect times out on unbounded plans
Submitted a 6-issue plan to the architect. Opus produced 1.2MB of output in 10 minutes before the session timeout killed it (exit 143). The architect tried to decompose everything at once. Direct expert questions were faster and more reliable for investigation. Architect excelled at bounded synthesis (consolidate 3 reports into one doc — completed in 3 minutes).

### Expert sessions hit MCP tool permission prompts
Headless expert sessions couldn't call `pool_send_response` because MCP tool names weren't in `--allowedTools`. The expert completed its work but couldn't return the result. Fixed by auto-appending pool MCP tool names to the allowed list when spawning.

### No daemon lifecycle management
No way to check if the daemon is running, stop it cleanly, or see what's happening. Had to `pkill -f` and stare at raw JSON logs. Filed as v0.6 milestone.

### pool_recall reads the wrong directory
The concierge calling `pool_recall` looks in its own log dir, not the expert's. Had to manually parse expert log files with a Python script.

### Claude Code now requires --verbose with stream-json
Expert sessions failed immediately with exit code 1. Quick fix: add `--verbose` flag to spawn args.

## Patterns that emerged

| Pattern | When it works |
|---------|---------------|
| Direct expert questions | Investigation, code review, domain queries |
| Parallel dispatch | Tasks touch different packages with no overlap |
| Architect for bounded synthesis | Clear input, clear output format, bounded scope |
| Architect for open-ended decomposition | Doesn't work — times out or goes too deep |

## Design decisions made

1. **Remove default session timeout** — timeouts kill productive work; Claude sessions naturally terminate
2. **Unix socket for daemon lifecycle** — industry standard (Docker, Consul, Nomad all do this), replaces pidfile+heartbeat design
3. **Roadmap reshuffled** — v0.6 is now daemon lifecycle (was v0.8 items), everything else shifted one slot
4. **Drop `pool_` prefix from tool names** — MCP already namespaces as `mcp__agent-pool__`, prefix is redundant

## By the numbers

- 9 expert tasks dispatched, 8 completed, 1 cancelled
- 2 architect tasks, 1 completed, 1 timed out
- 5 bugs fixed, 15 tests added, 502 lines across 12 files
- ~45 minutes wall clock from issue triage to PR
- 7 GitHub issues filed from dogfooding findings (#8-#16)
- 5 bugs fixed in agent-pool itself during the session
