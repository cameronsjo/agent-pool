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
- **Zero-dependency message bus** — filesystem + fsnotify, no databases or queues
- **Daemon-managed scheduling** — bookkeeping in Go, not in the LLM

## Quick Start

```bash
# Build
make build

# Create a pool
mkdir -p ~/.agent-pool/pools/my-project/{postoffice,contracts,formulas}
mkdir -p ~/.agent-pool/pools/my-project/{concierge,architect,researcher}
mkdir -p ~/.agent-pool/pools/my-project/experts/backend/{inbox,logs}

# Configure
cat > ~/.agent-pool/pools/my-project/pool.toml << 'EOF'
[pool]
name = "my-project"
project_dir = "~/Projects/my-project"

[architect]
model = "opus"

[experts.backend]
model = "sonnet"
EOF

# Start the daemon
bin/agent-pool start ~/.agent-pool/pools/my-project
```

## Architecture

See [docs/plans/architecture.md](docs/plans/architecture.md) for the full design document.

## How Agent Pool Compares

Agent Pool draws inspiration from two prominent multi-agent frameworks — [Gas Town](https://github.com/steveyegge/gastown) and [OpenClaw](https://github.com/openclaw/openclaw) — while making different architectural bets.

### Gas Town *(as of 2026-04-04)*

Steve Yegge's multi-agent workspace manager orchestrates 20-30 parallel coding agents through tmux sessions, git worktrees, and the Beads issue tracker. Agents coordinate via a Mad Max-themed role hierarchy (Mayor, Pole Cats, Refinery, Deacon, Witness) with a watchdog chain for health monitoring. It supports Claude, Gemini, Codex, Cursor, and others.

| | Gas Town | Agent Pool |
|---|---|---|
| **Scope** | Factory-scale swarms (20-30 agents) | Focused specialist pools (4-8 experts) |
| **Coordination** | tmux sessions + git worktrees + Beads | Filesystem mail + fsnotify + MCP server |
| **Activation** | GUPP (pull-based) | GUPP-inspired (mail delivery triggers spawn) |
| **State** | Dolt/SQLite data plane | Plain files (TOML config, Markdown state) |
| **Session model** | Long-running tmux sessions | Disposable headless `claude -p` per task |
| **Agent support** | Multi-provider (10+ agents) | Claude Code only (by design) |
| **Complexity** | ~189k LOC, Homebrew/npm install | ~5k LOC, single Go binary |

**What we took:** GUPP, handoff mechanics, log recall, formulas, named roles.
**What we left:** The scale ambition, Dolt data plane, multi-provider support, Mad Max branding.

### OpenClaw *(as of 2026-04-04)*

Peter Steinberger's personal AI assistant framework provides a persistent Gateway daemon as the control plane for multi-channel messaging (WhatsApp, Telegram, Slack, Discord, and 15+ more). Agents are defined through a workspace-as-brain model with SOUL.md identity files and a skills ecosystem. It supports Claude, GPT-4o, Gemini, and DeepSeek.

| | OpenClaw | Agent Pool |
|---|---|---|
| **Focus** | General-purpose personal assistant | Developer tooling (code-focused) |
| **Coordination** | Always-on Gateway daemon | On-demand process supervisor |
| **Identity** | SOUL.md + workspace file taxonomy | identity.md + state.md + errors.md |
| **Self-improvement** | Skill extraction + plugin ecosystem | Structured error capture + promotion ladder |
| **Session model** | Persistent Gateway with compaction | Fresh session per task, knowledge on disk |
| **Agent support** | Multi-LLM (Claude, GPT-4o, Gemini, DeepSeek) | Claude Code only |
| **Runtime** | Node.js (16GB RAM recommended) | Go binary (~20MB) |

**What we took:** Workspace-as-brain, self-improvement patterns, promotion ladder, compaction flush, identity split.
**What we left:** Always-on Gateway, plugin ecosystem, multi-LLM support, multi-channel messaging.

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
| **v0.4** | **In progress** | Architect — contracts, verification loop, role-aware MCP |
| v0.5 | Planned | Concierge plugin — user-facing Claude Code integration |
| v0.6 | Planned | Shared experts — cross-project knowledge, multi-pool |
| v0.7 | Planned | Researcher — curation, cold-start seeding |
| v0.8 | Planned | Formulas — workflow templates, operational hardening |

## License

MIT
