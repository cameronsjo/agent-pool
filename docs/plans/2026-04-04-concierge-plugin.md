# v0.5 — Concierge Plugin

## Context

v0.4 delivered the architect verification loop (contracts, approval gate, task delegation). Everything so far is daemon-side — headless processes coordinated via filesystem mail. There's no user-facing integration.

v0.5 adds the concierge layer: MCP tools that let the user's interactive Claude Code session dispatch questions to experts (read path) and submit plans to the architect (write path), plus a plugin scaffold with skills that guide users through these flows.

**Design decisions** (confirmed with Cameron):
- Poll taskboard for response waiting (reuses existing infra)
- `pool_submit_plan` returns immediately with task ID
- Plugin lives in-repo under `plugin/`
- User specifies experts explicitly for v0.5
- Concierge gets `identity.md` state file
- Expert responses read from task logs (full result text, untruncated)

---

## Step 1: Add `ExtractResult` to expert/log.go

**File:** `internal/expert/log.go`

Add `ExtractResult(output []byte) string` — same as `ExtractSummary` but returns full text without truncation. Collapses whitespace but does not truncate.

This is the function `pool_ask_expert` will use to extract the expert's answer from their session log.

---

## Step 2: Register concierge tools in MCP server

**File:** `internal/mcp/server.go` (modify)

Add concierge registration alongside the existing architect block:

```go
if cfg.Role == "concierge" {
    RegisterConciergeTools(srv, cfg)
}
```

Note: concierge also gets expert tools (read_state, update_state, etc.) since `RegisterExpertTools` is called unconditionally. The concierge's expertDir resolves to `{poolDir}/concierge/` via `mail.ResolveExpertDir`.

**File:** `internal/mcp/server.go` — fix expertDir resolution (pre-existing bug)

Line 24 hardcodes `filepath.Join(cfg.PoolDir, "experts", cfg.ExpertName)`. This is wrong for built-in roles — the architect's state dir is `{poolDir}/architect/`, not `{poolDir}/experts/architect/`. Switch to `mail.ResolveExpertDir(cfg.PoolDir, cfg.ExpertName)` which already handles the built-in role vs expert distinction. This fixes state tool paths for architect (existing) and enables them for concierge (new).

---

## Step 3: Implement concierge MCP tools

**New file:** `internal/mcp/concierge_tools.go`

`RegisterConciergeTools(srv *server.MCPServer, cfg *ServerConfig)` registers 4 tools:

### pool_ask_expert
- **Params:** `expert` (required), `question` (required)  
- **Flow:**
  1. Generate ID: `cq-{expert}-{unixnano}` (e.g., `cq-auth-1714847530000000000`)
  2. Compose `mail.Message{Type: TypeQuestion, From: "concierge", To: expert}`
  3. Write to `{poolDir}/postoffice/{id}.md`
  4. Poll `taskboard.json` every 2s with 5min timeout (same pattern as `approval.Gate.Request`)
  5. On completion: resolve expert dir via `mail.ResolveExpertDir`, read log with `expert.ReadLog`, extract full result with `expert.ExtractResult`
  6. On failure: return task status and exit code
  7. On timeout: return timeout error
- **Returns:** Expert's full result text

### pool_submit_plan  
- **Params:** `plan` (required), `contracts` (optional, comma-separated)
- **Flow:**
  1. Generate ID: `cp-{unixnano}`
  2. Compose `mail.Message{Type: TypeTask, From: "concierge", To: "architect", Contracts: parsed}`
  3. Write to postoffice
  4. Return immediately with task ID
- **Returns:** `"plan submitted (id: cp-...)"` — non-blocking

### pool_check_status
- **Params:** `task_id` (optional), `expert` (optional), `status` (optional)
- **Flow:**
  1. Load `taskboard.json`
  2. If `task_id`: return that task's full details (JSON)
  3. If `expert`: filter by `task.Expert`
  4. If `status`: filter by `task.Status`
  5. If no filters: return all non-terminal tasks
- **Returns:** JSON array of matching tasks
- **Annotation:** read-only hint

### pool_list_experts
- **Params:** none
- **Flow:**
  1. Load `pool.toml` via `config.LoadPool`
  2. Collect pool-scoped expert names from `cfg.Experts` map
  3. Collect shared expert names from `cfg.Shared.Include`
  4. Return formatted list
- **Returns:** JSON with `pool_experts` and `shared_experts` arrays
- **Annotation:** read-only hint

---

## Step 4: Plugin scaffold

**New directory:** `plugin/`

### plugin/plugin.json
```json
{
  "name": "agent-pool",
  "description": "Expert pool — delegate questions and tasks to domain specialists via a mixture-of-experts model",
  "version": "0.5.0"
}
```

### plugin/.mcp.json
```json
{
  "mcpServers": {
    "agent-pool": {
      "command": "agent-pool",
      "args": ["mcp", "--pool", "${AGENT_POOL_DIR}", "--role", "concierge"],
      "type": "stdio"
    }
  }
}
```

### plugin/skills/pool-ask.md
Read path skill. Guides the concierge to:
1. Use `pool_list_experts` to discover available experts
2. Identify which experts are relevant to the user's question
3. Dispatch questions in parallel via multiple `pool_ask_expert` calls
4. Synthesize expert responses into a unified answer

### plugin/skills/pool-build.md
Write path skill. Guides the concierge to:
1. Gather requirements from user
2. Optionally consult experts for domain input via `pool_ask_expert`
3. Synthesize input into a plan/spec
4. Submit via `pool_submit_plan`
5. Track progress via `pool_check_status`

### plugin/skills/pool-status.md
Status skill. Guides the concierge to:
1. Query taskboard via `pool_check_status`
2. Format results for human readability
3. Highlight blocked/failed tasks

---

## Step 5: Concierge identity

**New file:** `plugin/concierge-identity.md`

Template identity file for the concierge role. Installed to `{poolDir}/concierge/identity.md` when the pool is initialized. Defines the concierge's behavioral contract: delegation, synthesis, user advocacy.

---

## Step 6: Tests

**New file:** `internal/mcp/concierge_tools_test.go`

Test pattern: create temp pool directory with postoffice + taskboard + expert dirs, then exercise each tool handler directly.

### pool_ask_expert test
1. Set up pool dir with postoffice, expert inbox, expert logs dir
2. Call handler in a goroutine (it blocks polling)
3. In test goroutine: verify message appeared in postoffice, create taskboard entry as completed, write fake expert log
4. Verify handler returns the expected result text
5. Also test: timeout, failed task, missing expert dir

### pool_submit_plan test
1. Call handler
2. Verify message written to postoffice with correct type/fields
3. Verify returns immediately with task ID

### pool_check_status test
1. Pre-populate taskboard with various tasks
2. Test: single task lookup, expert filter, status filter, no-filter

### pool_list_experts test
1. Write pool.toml with experts and shared section
2. Verify correct expert lists returned

### ExtractResult test
1. Valid stream-json with result → full text returned
2. No result messages → fallback
3. Multiple results → last one wins

---

## Step 7: Update help text

**File:** `cmd/agent-pool/main.go`

Update `printUsage()` to show `--role concierge` as a valid option. Update version string.

---

## Files summary

| Action | File | Purpose |
|--------|------|---------|
| Modify | `internal/expert/log.go` | Add `ExtractResult()` |
| Modify | `internal/mcp/server.go` | Concierge registration + expertDir fix |
| Create | `internal/mcp/concierge_tools.go` | 4 tool handlers |
| Create | `internal/mcp/concierge_tools_test.go` | Tool tests |
| Create | `plugin/plugin.json` | Plugin manifest |
| Create | `plugin/.mcp.json` | MCP server config |
| Create | `plugin/skills/pool-ask.md` | Read path skill |
| Create | `plugin/skills/pool-build.md` | Write path skill |
| Create | `plugin/skills/pool-status.md` | Status skill |
| Create | `plugin/concierge-identity.md` | Concierge identity template |
| Modify | `cmd/agent-pool/main.go` | Help text + version |

**Reused functions:**
- `mail.Compose()` (`internal/mail/compose.go`) — message composition
- `mail.ResolveExpertDir()` (`internal/mail/router.go:26`) — expert dir resolution
- `taskboard.Load()` (`internal/taskboard/store.go`) — taskboard reading
- `expert.ReadLog()` (`internal/expert/state.go:101`) — log reading
- `config.LoadPool()` (`internal/config/config.go:80`) — config loading

---

## Verification

1. `make check` — vet + lint + test all pass
2. Manual MCP test:
   ```
   echo '{}' | agent-pool mcp --pool /tmp/test-pool --role concierge
   ```
   Verify concierge tools appear in tool listing (expert tools + 4 concierge tools)
3. Tool handler tests verify:
   - `pool_ask_expert`: message written to postoffice, polls taskboard, returns result
   - `pool_submit_plan`: message written, returns immediately
   - `pool_check_status`: filters work correctly
   - `pool_list_experts`: reads pool.toml correctly
4. Plugin files are valid JSON and skills contain correct tool references
