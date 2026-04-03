---
name: pool-status
description: Show current state of a pool (queued messages, expert status, recent logs)
disable-model-invocation: true
---

# pool-status

Show the current state of an agent-pool.

## Usage

`/pool-status [--pool <path>]`

## Defaults

- `--pool`: Value of `AGENT_POOL_DIR` env var, or prompt if unset

## Workflow

1. Read pool directory structure
2. For each section, summarize state:

### Postoffice

- Count of `.md` files waiting to be routed
- List filenames if any exist

### Experts

For each expert directory under `{pool}/experts/`:

- **Inbox**: count of queued messages (list IDs if any)
- **Logs**: count of completed tasks, show last 5 entries from `logs/index.md`
- **State files**: which of `identity.md`, `state.md`, `errors.md` exist

### Output Format

```
Pool: {name} ({path})

Postoffice: {n} pending
  - task-001.md
  - task-002.md

Experts:
  auth:
    Inbox: 0 queued
    Logs: 12 tasks (last: task-auth-042 — "Implemented token endpoint")
    State: identity.md, state.md, errors.md

  frontend:
    Inbox: 1 queued (task-frontend-007.md)
    Logs: 3 tasks (last: task-frontend-003 — "Added form validation")
    State: identity.md
```
