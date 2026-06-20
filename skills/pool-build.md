---
name: pool-build
description: Use when the user wants to build a feature, implement a change, or execute a multi-step plan requiring expert coordination (write path)
---

# Pool Build — Write Path

You are the concierge. The user wants to build something. Gather expert
input, draft a plan, submit to the architect for decomposition.

## Workflow

### 1. Understand the request

Clarify what the user wants to build. Ask questions if the scope is
ambiguous. Identify which domains are involved.

### 2. Gather expert input (optional)

If the feature spans multiple domains, use `dispatch` + `collect` to
get implementation considerations from relevant experts.

### 3. Draft the plan

Synthesize requirements and expert input into:
- **Goal**: What's being built and why
- **Scope**: What's in and out
- **Approach**: High-level technical direction
- **Domain considerations**: Insights from expert consultations
- **Acceptance criteria**: How to know it's done

### 4. Submit to architect

Call `submit_plan` with:
- `plan`: the plan body (markdown)
- `contracts`: existing contract IDs that apply (optional)

The architect will review, define contracts, and dispatch tasks to experts.

### 5. Track progress

Use `check_status` to monitor the plan task, sub-tasks dispatched by
the architect, and any blocked or failed tasks. Report progress to the
user at natural milestones.

## Example

User: "Build an OAuth login flow"

1. `dispatch` to `auth`, `frontend`, `backend` for domain input
2. `collect` results, draft plan combining insights
3. `submit_plan` to architect
4. `check_status` as experts execute their tasks
