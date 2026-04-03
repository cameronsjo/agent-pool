---
name: pool-submit
description: Submit a task to an agent-pool expert via the postoffice
disable-model-invocation: true
---

# pool-submit

Submit a task message to a pool's postoffice directory.

## Usage

`/pool-submit <expert> <body> [--pool <path>] [--from <role>] [--type <type>] [--priority <priority>]`

## Defaults

- `--pool`: Value of `AGENT_POOL_DIR` env var, or prompt if unset
- `--from`: `architect`
- `--type`: `task`
- `--priority`: `normal`

## Workflow

1. Parse arguments. Expert name and body are required.
2. Generate a message ID: `task-{expert}-{timestamp}` (e.g., `task-auth-20260403-143200`)
3. Validate the ID is filename-safe (no path separators, not `.` or `..`)
4. Build the YAML frontmatter + markdown body
5. Write to `{pool}/postoffice/{id}.md`
6. Confirm: "Submitted {id} to {expert} via postoffice"

## Message Format

```markdown
---
id: {generated-id}
from: {from}
to: {expert}
type: {type}
priority: {priority}
timestamp: {UTC ISO 8601}
---

{body}
```

## Validation

- Expert name must be non-empty and filename-safe
- Body must be non-empty
- Pool directory must exist and contain a `postoffice/` subdirectory
- Message type must be one of: task, question, response, notify, handoff, cancel
