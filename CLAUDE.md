# Agent Pool

## What This Is

A Go process supervisor that manages headless Claude Code expert sessions using a mixture-of-experts model. Four roles — concierge (PM), architect (tech lead), experts (domain specialists), researcher (enrichment) — coordinate via filesystem-based mail delivery with externalized state replacing conversation persistence.

## Architecture

Full design: `docs/plans/architecture.md`

### Roles

| Role | Model | Session | Function |
|------|-------|---------|----------|
| Concierge | Sonnet | Interactive | User-facing PM, delegates everything |
| Architect | Opus | Headless | Defines contracts, delegates tasks, verifies |
| Expert | Sonnet | Headless | Domain work, maintains knowledge |
| Researcher | Sonnet/Haiku | Headless | Curation, enrichment, research |

### Directory Layout

```
~/.agent-pool/
├── config.toml                    # Global defaults
├── experts/                       # Shared experts (reusable across pools)
└── pools/
    └── {pool-name}/
        ├── pool.toml              # Pool config
        ├── taskboard.json         # Daemon-managed task state
        ├── postoffice/            # Central mail drop
        ├── contracts/             # Architect-managed interface specs
        ├── experts/{name}/        # Pool-scoped experts
        └── shared-state/{name}/   # Project overlays for shared experts
```

### Integration Model

Agent Pool builds ON Claude Code via external interfaces — it does NOT modify Claude Code source.

- **CLI**: `claude -p --output-format stream-json --model sonnet --allowedTools "..."`
- **MCP server**: `agent-pool mcp --pool {name} --expert {name}` for typed pool tools
- **Hooks**: Stop → flush, PreToolUse → code ownership guard
- **Plugin**: Skills for concierge workflows
- **Env vars**: `AGENT_POOL_NAME`, `AGENT_POOL_EXPERT`, `AGENT_POOL_TASK_ID`

## Project Structure

```
cmd/agent-pool/       CLI entry point
internal/
  config/             pool.toml parsing and validation
  daemon/             Process supervisor, fsnotify, lifecycle management
  mail/               Message parsing, routing, delivery
  expert/             Session spawning, prompt assembly, state management
  mcp/                MCP server (stdio, per-role tool sets)
docs/
  plans/              Architecture and development plans
  adr/                Architecture Decision Records
scripts/              Build and utility scripts
```

## Development

```bash
make build            # Build binary to bin/agent-pool
make dev POOL=<path>  # Quick iteration with go run
make test             # Run all tests
make test-cover       # Coverage report (HTML)
make test-gaps        # Show functions below 70% (override with THRESHOLD=N)
make check            # vet + lint + test
```

## Implementation Status

**v0.2 complete** — MCP + State Management:

- [x] fsnotify watching postoffice
- [x] Mail routing (parse YAML header, copy to inbox)
- [x] Expert spawning via `claude -p`
- [x] Log capture to `logs/{task-id}.json`
- [x] Manual task submission (write .md to postoffice/)
- [x] MCP server (stdio, per-expert) with expert tool set
- [x] `pool_update_state`, `pool_append_error`, `pool_read_state`
- [x] `pool_send_response`, `pool_recall`, `pool_search_index`
- [x] Stop hook for flush guarantee (`agent-pool flush`)
- [x] PreToolUse hook for code ownership guard (`agent-pool guard`)
- [x] MCP config generation + spawn integration (`--mcp-config`)

Next: **v0.3** — Task Board + Dependencies

See `docs/plans/architecture.md` § Implementation Phasing for v0.1–v0.8 roadmap.

## Code Conventions

- **Go style**: Follow standard Go conventions (`gofmt`, `goimports`)
- **Error handling**: Wrap errors with `fmt.Errorf("context: %w", err)`
- **Logging**: Structured JSON via `slog` (stdlib)
- **Config**: TOML for human-edited files, JSON for daemon-managed state
- **Mail format**: Markdown with YAML frontmatter
- **Testing**: Integration tests preferred over unit tests
- **Test plans**: Comment block at top of each test file documenting coverage matrix
- **Test doubles**: Chicago-school (real objects) by default. London-school (fakes) only at I/O boundaries via interface injection

## Gotchas

- **Message IDs must be filename-safe** — used as filenames in routing and logging. `mail.Parse` rejects path separators, `.`, `..`
- **At-least-once delivery** — router copies then deletes. Crash = possible duplicate. Experts should be idempotent
- **Non-zero expert exit preserves inbox file** — stays for retry/inspection. Logs always written regardless of exit code
- **Daemon drains on startup** — pre-existing postoffice and inbox files are processed when the daemon starts
- **`Spawner` interface** — `daemon.Daemon` accepts `WithSpawner(s)` for test injection. Use `fakeSpawner` pattern in tests
- **Architecture doc is source of truth** — `docs/plans/architecture.md`, not external copies
