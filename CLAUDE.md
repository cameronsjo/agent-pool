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
- **MCP server**: `agent-pool mcp --pool {name} --expert {name}` (experts) or `--role {architect|concierge}` (built-in roles)
- **Hooks**: Stop → flush, PreToolUse → code ownership guard
- **Plugin**: repo root — skills (`pool-ask`, `pool-build`, `pool-status`, `pool-research`) + `.mcp.json` for concierge
- **Env vars**: `AGENT_POOL_NAME`, `AGENT_POOL_EXPERT`, `AGENT_POOL_TASK_ID`

## Project Structure

```
cmd/agent-pool/       CLI entry point
internal/
  approval/           Human approval gate (filesystem-based polling)
  atomicfile/         Atomic file writes (temp + fsync + rename)
  config/             pool.toml parsing and validation
  contract/           Versioned interface specs between experts
  daemon/             Process supervisor, fsnotify, lifecycle management
  expert/             Session spawning, prompt assembly, state management
  mail/               Message parsing, routing, delivery
  mcp/                MCP server (stdio, per-role tool sets)
  taskboard/          DAG-based task tracking with dependency evaluation
skills/               Claude Code plugin skills (pool-ask, pool-build, pool-status, pool-research)
.claude-plugin/       Plugin manifest for marketplace
.mcp.json             MCP server config for concierge role
concierge-identity.md Concierge role identity
docs/
  plans/              Architecture and development plans
  prompts/            Version-specific development prompts
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

**v0.8 complete** — through Researcher + Curation. v0.9 (formulas + polish) in progress. See `docs/plans/architecture.md` § Implementation Phasing for full roadmap.

| Version | Milestone | Key Additions |
|---------|-----------|---------------|
| v0.2 | MCP + State | Expert tools, mail routing, spawning, hooks |
| v0.3 | Task Board | DAG dependencies, cancel/handoff, session timeout |
| v0.4 | Architect | Contracts, approval gate, task delegation, verification |
| v0.5 | Concierge | Concierge MCP tools, plugin scaffold, read/write path flows |
| v0.6 | Daemon Lifecycle | Unix socket, stop/status/watch, graceful drain |
| v0.7 | Shared Experts | Cross-project knowledge, multi-pool, project overlays |
| v0.8 | Researcher | Curation, pattern promotion, cold-start seeding |

Next: **v0.9** — Formulas + Polish

## Code Conventions

- **Module**: `github.com/cameronsjo/agent-pool`
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
- **`handleInbox` runs in goroutines** — daemon tests must wait for task completion before shutdown, or TempDir cleanup races with log writes. Use `waitForTaskStatus` helper
- **Built-in role dirs are top-level** — `{poolDir}/architect/`, `{poolDir}/concierge/`, NOT under `experts/`. Use `mail.ResolveExpertDir` to resolve correctly
- **MCP test helpers live in `testhelp_test.go`** — `makePoolDirs`, `buildMCPTestServer`, `callTool`, `resultText`. Don't duplicate in new test files
- **`postMessage` helper** — use `mcp.postMessage(poolDir, msg)` for all postoffice writes. Handles compose + MkdirAll + atomic write
- **Architecture doc is source of truth** — `docs/plans/architecture.md`, not external copies
