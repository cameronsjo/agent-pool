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

## Development Status

Currently in **v0.1** — building the basic expert lifecycle loop.

## License

MIT
