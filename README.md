# Agent Pool

Process supervisor managing headless Claude Code expert sessions with mixture-of-experts routing, externalized state, and filesystem-based coordination.

## What Is This?

Agent Pool manages a **pool** of domain-specialist Claude Code sessions. Each expert boots from externalized state (`identity.md`, `state.md`, `errors.md`), does work, updates its knowledge, and dies. The session is disposable. The knowledge isn't.

Four roles coordinate the work:

| Role | Function |
|------|----------|
| **Concierge** | Product manager — user-facing, builds plans, synthesizes results |
| **Architect** | Tech lead — defines contracts between experts, delegates tasks, verifies output |
| **Expert** | Domain specialist — executes tasks, maintains domain knowledge |
| **Researcher** | Enrichment — curates expert knowledge, seeds new experts, runs research |

## Key Properties

- **Zero tokens at rest** — sleeping experts cost nothing
- **Contracts before code** — architect defines interfaces between experts before work starts
- **Knowledge has a lifecycle** — learnings graduate from logs to state to identity
- **Pools + shared experts** — project-scoped pools with reusable cross-project specialists
- **Zero-infrastructure message bus** — filesystem + fsnotify, no databases or queues
- **Daemon-managed scheduling** — bookkeeping in Go, not in the LLM

## Quick Start

```bash
# Build and install
make build
ln -sf $(pwd)/bin/agent-pool ~/bin/agent-pool

# Create a pool inside your project
cd ~/Projects/my-project
mkdir -p .agent-pool

# Configure
cat > .agent-pool/pool.toml << 'EOF'
[pool]
name = "my-project"
project_dir = "~/Projects/my-project"

[architect]
model = "opus"

[experts.backend]
model = "sonnet"
EOF

# Start the daemon (auto-discovers .agent-pool/ from cwd)
agent-pool start

# Check daemon status
agent-pool status

# Stop gracefully
agent-pool stop
```

### Claude Code Plugin

Install from the marketplace:

```bash
/plugin marketplace add cameronsjo/workbench
/plugin install agent-pool@cameronsjo
```

Or point to a local build:

```bash
/plugin install /path/to/agent-pool
```

## Architecture

See [docs/plans/architecture.md](docs/plans/architecture.md) for the full design document.

## How Agent Pool Compares

Agent Pool draws inspiration from two prominent multi-agent frameworks — [Gas Town](https://github.com/steveyegge/gastown) and [OpenClaw](https://github.com/openclaw/openclaw) — while making different architectural bets. *(Comparisons as of 2026-04-04.)*

| | [Gas Town](https://github.com/steveyegge/gastown) | [OpenClaw](https://github.com/openclaw/openclaw) | **Agent Pool** |
|---|---|---|---|
| **Focus** | Parallel coding at scale | General-purpose personal assistant | Domain-specialist dev workflows |
| **Scale** | 20-30 agents per swarm | Single agent, multi-channel | 4-8 focused experts per pool |
| **Coordination** | tmux + git worktrees + Beads | Always-on Gateway daemon | Filesystem mail + fsnotify |
| **Activation** | GUPP (pull-based) | Channel routing | GUPP-inspired (mail triggers spawn) |
| **State** | Dolt/SQLite data plane | Persistent Gateway + compaction | Plain files (TOML + Markdown) |
| **Session model** | Long-running tmux sessions | Persistent with compaction | Disposable `claude -p` per task |
| **Identity** | Per-rig config | SOUL.md + workspace files | identity.md + state.md + errors.md |
| **Agent support** | Multi-provider (10+) | Multi-LLM (Claude, GPT-4o, Gemini) | Claude Code only (by design) |
| **Tool system** | CLI commands + file drops | Skills (SKILL.md) + plugins | MCP server (per-role tool sets) |
| **Runtime** | Go (~189k LOC) | Node.js (16GB RAM rec.) | Go binary (~5k LOC, ~20MB) |
| **Contract system** | Beads (issues) | None | Architect-managed interface specs |
| **Role hierarchy** | Mayor, Pole Cats, Refinery, Deacon, Witness | Flat (peer agents) | Concierge, Architect, Expert, Researcher |

### Design Lineage

**From [Gas Town](https://github.com/steveyegge/gastown):** GUPP, handoff mechanics, log recall, formulas, named roles.
Left behind: scale ambition, Dolt data plane, multi-provider support.

**From [OpenClaw](https://github.com/openclaw/openclaw):** Workspace-as-brain, self-improvement patterns, promotion ladder, compaction flush, identity split.
Left behind: always-on Gateway, plugin ecosystem, multi-LLM support, multi-channel messaging.

See [architecture doc § Design Lineage](docs/plans/architecture.md#design-lineage) for full provenance and references.

### Agent Pool's Niche

Gas Town optimizes for **scale** — managing swarms of agents across providers. OpenClaw optimizes for **reach** — connecting to every messaging platform and LLM. Agent Pool optimizes for **depth** — a small number of Claude Code specialists that build durable domain knowledge across tasks, coordinated by contracts and verified by an architect. The bet is that four focused experts with persistent knowledge outperform thirty ephemeral agents for most real development work.

## Development

```bash
make build            # Build binary to bin/agent-pool
make dev POOL=<path>  # Quick iteration with go run
make test             # Run all tests
make test-cover       # Coverage report (HTML)
make test-gaps        # Show functions below 70% (override with THRESHOLD=N)
make check            # vet + lint + test
```

## Development Status

| Version | Status | What |
|---------|--------|------|
| **v0.1** | Complete | Expert lifecycle — mail in, expert runs, mail out |
| **v0.2** | Complete | MCP server — typed tools for state management, hooks |
| **v0.3** | Complete | Task board — dependency DAG, cancellation, health checks |
| **v0.4** | Complete | Architect — contracts, verification loop, role-aware MCP |
| **v0.5** | Complete | Concierge plugin — MCP tools, skills, read/write path flows |
| **v0.6** | Complete | Daemon lifecycle — unix socket, stop/status/watch, graceful drain |
| **v0.7** | Complete | Shared experts — cross-project knowledge, multi-pool |
| **v0.8** | Complete | Researcher — curation, cold-start seeding, pattern promotion |
| v0.9 | In progress | Formulas — workflow templates, config hot-reload |

## License

MIT
