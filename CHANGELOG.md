# Changelog

All notable changes to Agent Pool are documented here.

## [Unreleased]

## [0.9.0] — 2026-04-07

Formulas and operational hardening.

- Formula parsing (`internal/formula/`) with TOML templates, DAG validation
- `instantiate_formula` architect MCP tool for bulk task creation
- Config hot-reload via fsnotify on `pool.toml`
- `EventConfigReloaded` event type
- `formulas/` directory in pool structure

## [0.8.0] — 2026-04-06

Researcher role for knowledge curation and enrichment.

- Researcher MCP tools: `enrich_state`, `write_expert_state`, `promote_pattern`, `read_expert_state`, `read_expert_logs`, `list_experts` (researcher variant)
- Daemon curation scheduling with configurable intervals
- Pattern promotion: durable patterns graduate from `state.md` to `identity.md`
- Researcher identity and prompt assembly
- Claude Code plugin: added `pool-research` skill

## [0.7.0] — 2026-04-06

Shared experts and multi-pool foundations.

- `~/.agent-pool/experts/` for cross-project shared experts
- `shared.include` config in `pool.toml`
- Project overlay via `shared-state/{name}/` for pool-specific context
- Shared expert directory resolution in mail routing
- `list_experts` tool returns both pool-scoped and shared experts

## [0.6.0] — 2026-04-05

Daemon lifecycle and observability, driven by dogfooding.

- Unix domain socket for CLI-to-daemon communication
- `agent-pool stop` — graceful shutdown via socket
- `agent-pool status` — daemon health and task summary
- `agent-pool watch` — live event stream
- Structured event bus (`internal/daemon/events.go`)
- Graceful drain on SIGINT/SIGTERM with configurable timeout
- `daemon.log` file output by default

## [0.5.0] — 2026-04-04

Concierge plugin — user-facing interface for the pool.

- Concierge MCP tools: `dispatch`, `collect`, `ask_expert`, `submit_plan`, `check_status`, `list_experts`
- Claude Code plugin with skills: `pool-ask`, `pool-build`, `pool-status`
- `.mcp.json` for auto-registering concierge MCP server
- `concierge-identity.md` role prompt
- Non-blocking `dispatch` + `collect` pattern for parallel expert queries

## [0.4.0] — 2026-04-03

Architect role — contracts, verification, and task delegation.

- Architect MCP tools: `define_contract`, `send_task`, `verify_result`, `amend_contract`
- Versioned contract specs in `contracts/` directory
- Human approval gate (`internal/approval/`) for architect-proposed changes
- Role-aware MCP server (different tool sets per role)

## [0.3.0] — 2026-04-02

Task board with dependency DAG.

- `internal/taskboard/` — DAG-based task tracking
- Dependency evaluation (`EvaluateDeps`) with cycle detection
- Task states: pending, blocked, active, completed, failed, cancelled
- Cancel propagation through dependency chains
- Session timeout and health checks

## [0.2.0] — 2026-04-01

MCP server and state management.

- Expert MCP tools: `read_state`, `update_state`, `append_error`, `send_response`, `recall`, `search_index`
- Mail composition and routing via MCP
- Expert spawning with `claude -p` integration
- Pre-tool-use hooks for code ownership guards
- CLI wiring for `agent-pool mcp` subcommand

## [0.1.0] — 2026-03-31

Expert lifecycle — the foundation.

- Mail parsing with YAML frontmatter
- Filesystem-based message routing (postoffice model)
- Expert session spawning with identity, state, and error context
- Log capture and task indexing
- At-least-once delivery with crash-safe inbox handling
- `Spawner` interface for test injection
