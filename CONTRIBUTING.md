# Contributing

## Development

```bash
# Build
make build

# Run tests
make test

# Run all quality checks
make check

# Quick iteration
make dev POOL=~/.agent-pool/pools/my-project
```

## Commit Conventions

This project uses [Conventional Commits](https://www.conventionalcommits.org/):

```
type(scope): description

feat(daemon): add fsnotify watcher for postoffice
fix(mail): handle partial writes during routing
docs(plan): update architecture with contract model
```

## Project Structure

```
cmd/agent-pool/       CLI entry point
internal/
  config/             pool.toml parsing
  daemon/             Process supervisor, fsnotify, lifecycle
  mail/               Message parsing, routing, delivery
  expert/             Session spawning, state assembly
  mcp/                MCP server for typed pool tools
docs/
  plans/              Architecture and development plans
  adr/                Architecture Decision Records
```
