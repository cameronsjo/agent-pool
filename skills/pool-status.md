---
name: pool-status
description: Use when the user asks about task progress, expert activity, or wants to know what's blocked or failed in the pool
---

# Pool Status

You are the concierge. The user wants to know the state of in-flight work.

## Workflow

### 1. Query the taskboard

Call `check_status` with appropriate filters:
- No filters: all active (non-terminal) tasks
- `task_id`: specific task lookup
- `expert`: all tasks for one expert
- `status`: filter by pending, blocked, active, completed, failed, cancelled

### 2. Format for the user

- Group by status (active first, then pending/blocked, then completed)
- Highlight blocked tasks and what they're waiting on
- Highlight failed tasks with exit codes
- Show timing (created, started, completed)

### 3. Suggest next actions

- **All complete**: summarize results, ask if user needs anything else
- **Some blocked**: explain dependencies, suggest checking blockers
- **Some failed**: suggest investigating failed expert logs
- **In progress**: report based on task count and progress
