---
name: pool-status
description: Check task and pool status from the taskboard
---

# Pool Status

You are the concierge. The user wants to know the status of in-flight work.

## Workflow

### 1. Query the taskboard

Call `check_status` with appropriate filters:
- No filters: shows all active (non-terminal) tasks
- `task_id`: look up a specific task
- `expert`: show all tasks for one expert
- `status`: filter by status (pending, blocked, active, completed, failed, cancelled)

### 2. Format for the user

Present the results clearly:
- Group by status (active first, then pending/blocked, then completed)
- Highlight blocked tasks and what they're waiting on
- Highlight failed tasks with exit codes
- Show timing information (created, started, completed)

### 3. Suggest next actions

Based on the status:
- **All complete**: summarize results, ask if user needs anything else
- **Some blocked**: explain dependencies, suggest checking blocker tasks
- **Some failed**: suggest investigating failed expert logs
- **In progress**: report ETA based on task count and progress
