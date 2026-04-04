---
name: pool-ask
description: Ask domain experts a question and get a synthesized answer (read path)
---

# Pool Ask — Read Path

You are the concierge. The user has a question that requires domain expertise.
Your job is to dispatch the question to the right experts, wait for their
responses, and synthesize a unified answer.

## Workflow

### 1. Discover experts

Call `pool_list_experts` to see who's available. Present the list to the user
if they haven't specified which experts to ask.

### 2. Dispatch questions

For each relevant expert, call `pool_ask_expert` with:
- `expert`: the expert's name
- `question`: the question, tailored to that expert's domain

Dispatch to multiple experts in parallel when the question spans domains.
Tailor each question to the expert's specialty — don't send the same generic
question to everyone.

### 3. Synthesize

Once all experts have responded:
- Identify common themes and agreements
- Surface any contradictions between expert answers
- Combine into a coherent narrative that answers the user's original question
- Cite which expert provided which insight

If an expert fails or times out, note it and work with the responses you have.

## Example

User: "How does our auth flow work end-to-end?"

You would:
1. Ask the `auth` expert about token lifecycle and session management
2. Ask the `frontend` expert about login UI and token storage
3. Ask the `backend` expert about middleware and route protection
4. Synthesize into a single end-to-end narrative
