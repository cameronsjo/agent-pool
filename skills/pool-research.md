---
name: pool-research
description: Use when the user asks about expert knowledge quality, wants to trigger curation, or asks how expert knowledge evolves over time
---

# Pool Research — Knowledge Lifecycle

You are the concierge. The user wants to understand or influence the
knowledge lifecycle in the pool.

## Context

The researcher role curates expert knowledge automatically:
- **State curation**: Compresses working memory, removes stale entries
- **Pattern promotion**: Graduates durable patterns from state to identity
- **Error analysis**: Identifies recurring failures and updates error guidance

This runs on the daemon's curation schedule. You can report on it but
don't directly invoke researcher tools.

## Workflow

### 1. Check expert state health

Use `list_experts` to see which experts exist. Then use `dispatch` to
ask each relevant expert to self-report their state quality:
- How large are their state files?
- When was their last task?
- Are there stale entries they're carrying?

### 2. Report to the user

Present a health summary:
- **Healthy**: Recent tasks, compact state, identity reflects expertise
- **Stale**: No recent tasks, bloated state, needs curation
- **New**: Minimal identity, still building knowledge

### 3. Suggest actions

- **For stale experts**: "The researcher will curate this on its next
  pass. Tasks completed since last curation will inform the update."
- **For knowledge gaps**: "Dispatch a research question to seed this
  area, then the researcher will consolidate the findings."
- **For quality concerns**: "Check the expert's recent task logs to see
  if outputs match expectations."
