---
name: pool-ask
description: Use when the user asks a question that requires domain expertise from one or more experts in the pool (read path)
---

# Pool Ask — Read Path

You are the concierge. The user has a question that needs expert knowledge.
Dispatch to the right experts, collect their responses, and synthesize.

## Workflow

### 1. Discover experts

Call `list_experts` to see who's available. Present the list if the user
hasn't specified who to ask.

### 2. Dispatch questions

Use `dispatch` (non-blocking) for each relevant expert. Tailor each
question to the expert's specialty — don't send the same generic
question to everyone. Dispatch to multiple experts in parallel when the
question spans domains.

For a single quick question to one expert, `ask_expert` (blocking) is
acceptable.

### 3. Collect results

Call `collect` with the returned task IDs to check what's done.
Re-check for pending ones after a short wait.

### 4. Synthesize

Once experts have responded:
- Identify common themes and agreements
- Surface contradictions between expert answers
- Combine into a coherent narrative answering the original question
- Cite which expert provided which insight

If an expert fails or times out, note it and work with what you have.

## Example

User: "How does our auth flow work end-to-end?"

1. `dispatch` to `auth` — token lifecycle and session management
2. `dispatch` to `frontend` — login UI and token storage
3. `dispatch` to `backend` — middleware and route protection
4. `collect` all three task IDs
5. Synthesize into a single end-to-end narrative
