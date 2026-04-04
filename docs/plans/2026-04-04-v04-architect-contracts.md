# v0.4 — Architect + Contracts

## Context

v0.3 (taskboard + dependencies) is merged on main. The daemon now has durable task tracking, DAG evaluation, and process supervision. v0.4 adds the verification loop: an architect role that defines contracts between experts, delegates tasks, and verifies output. This is the first role beyond simple experts, introducing contract management, role-aware MCP tools, and a human approval gate.

## Key Design Decisions

**D1: Shared frontmatter extraction.** Both `mail.Parse` and the new `contract.Parse` need YAML frontmatter splitting. Extract `splitFrontmatter` from `internal/mail/mail.go` into `internal/frontmatter/frontmatter.go` as `Split()`. Pure refactor — mail tests verify no regression.

**D2: Approval gate lives in the MCP tool handler.** The `pool_send_task` handler blocks the architect session while waiting for human approval. The daemon owns the terminal (stdout/stdin), so the tool handler writes a proposal file to `{poolDir}/approvals/` and polls for a response file. The daemon watches that directory and presents proposals to the human. This keeps the daemon non-blocking for other events while the architect waits.

**D3: Amendment fan-out via postoffice.** `pool_amend_contract` writes the updated contract AND writes `notify` messages to the postoffice for each party in `between[]`. The daemon routes them normally — no contract directory watching needed. `TypeNotify` is already excluded from taskboard registration.

**D4: Role-aware MCP server.** Add `--role` flag to `agent-pool mcp`. When `--role architect`, register expert tools + architect tools. `ServerConfig` gets a `Role` field. `WriteTempConfig` gets a role-aware variant.

**D5: All three approval modes in v0.4.** The daemon runs `handleApprovalRequest` in a goroutine so the event loop continues. Implement "none", "stdout", and "file" modes. Stdout: daemon prints to terminal, reads y/n. File: daemon writes proposal to configured directory, watches for response file to appear.

---

## Phase 1: Frontmatter + Contract Package

**New files:**
- `internal/frontmatter/frontmatter.go` — `Split(content string) (header, body string, err error)`
- `internal/frontmatter/frontmatter_test.go`
- `internal/contract/contract.go` — `Contract` struct, `Parse`, `Compose`, `ParseFile`
- `internal/contract/store.go` — `Store` struct with `Save`, `Load`, `List`, `UpdateIndex`
- `internal/contract/contract_test.go`

**Modified files:**
- `internal/mail/mail.go` — replace `splitFrontmatter` body with call to `frontmatter.Split()`

**Contract struct:**
```go
type Contract struct {
    ID        string    `yaml:"id"`
    Type      string    `yaml:"type"`        // always "contract"
    DefinedBy string    `yaml:"defined-by"`
    Between   []string  `yaml:"between"`
    Version   int       `yaml:"version"`
    Timestamp time.Time `yaml:"timestamp"`
    Body      string    `yaml:"-"`
}
```

**Store** holds `contractsDir` (`{poolDir}/contracts/`). `Save` writes `{id}.md`, updates `index.md`. `Load` reads by ID. Validate: `id` filename-safe, `between` >= 2 entries, `version` >= 1.

**Index format** (`contracts/index.md`):
```
| ID | Between | Version | Timestamp |
|----|---------|---------|-----------|
| contract-007 | auth, frontend | 1 | 2026-04-01T14:32:00Z |
```

---

## Phase 2: Architect MCP Tools

**Modified files:**
- `internal/mcp/server.go` — add `Role` field to `ServerConfig`, register architect tools when `Role == "architect"`
- `internal/mcp/tools.go` — add `RegisterArchitectTools(srv, cfg)` with four tools
- `internal/mcp/config.go` — add `WriteTempConfigForRole(poolDir, role string)` that uses `--role` instead of `--expert`
- `cmd/agent-pool/main.go` — `cmdMCP()` accepts `--role` flag, `parseFlags` updated

**New files:**
- `internal/mcp/architect_tools.go` — tool handlers (keep separate from expert tools for clarity)
- `internal/mcp/architect_tools_test.go`

**Four architect tools:**

| Tool | Params | Action |
|------|--------|--------|
| `pool_define_contract` | `id`, `between` (comma-sep), `body` | Create contract v1, write to contracts/, update index |
| `pool_send_task` | `to`, `body`, `id`, `contracts` (opt), `depends_on` (opt), `priority` (opt) | Compose task message, write to postoffice (with approval gate) |
| `pool_verify_result` | `task_id`, `contract_id`, `status` (pass/fail/partial), `notes` | Log verification entry to architect's logs |
| `pool_amend_contract` | `id`, `body` | Load contract, bump version, save, write notify messages to postoffice |

The architect also inherits all 6 expert tools (read_state, update_state, append_error, send_response, recall, search_index) via the existing `RegisterExpertTools` call.

**MCP server registration in `Run()`:**
```go
RegisterExpertTools(srv, cfg)
if cfg.Role == "architect" {
    RegisterArchitectTools(srv, cfg)
}
```

---

## Phase 3: Architect Spawning

**Modified files:**
- `internal/daemon/daemon.go`
- `internal/mail/router.go` — export `IsBuiltinRole(name string) bool`

**Daemon changes:**

1. **`Run()`** — watch `architect/inbox/` via `watcher.Add()`
2. **`resolveExpertName()`** — check `mail.IsBuiltinRole(name)` and match architect inbox path
3. **`resolveExpertConfig()`** — if `name == "architect"`, use `d.cfg.Architect.Model`; return default tools
4. **`resolveExpertDir()`** — new helper: builtin roles use `{poolDir}/{role}`, experts use `{poolDir}/experts/{name}`
5. **`processInboxMessage()`** — use `resolveExpertDir()` for expert dir path; use `WriteTempConfigForRole` when role is builtin
6. **`ensureDirs()`** — add `contracts/`, `approvals/`, `architect/logs/`
7. **`drainAllInboxes()`** — add `go d.handleInbox(ctx, "architect", "")`
8. **Session timeout** — resolve from `d.cfg.Architect.SessionTimeout` when name is "architect"

---

## Phase 4: Contract Amendment Fan-out

Mostly handled by Phase 2's `pool_amend_contract` writing notify messages to the postoffice. The daemon routes them normally.

**Verification:**
- `TypeNotify` already excluded from `registerTask()` (line 196 of daemon.go)
- `ResolveInbox` already routes to expert inboxes correctly
- No daemon code changes needed

**Test:** Write a notify message to postoffice, verify it arrives in the expert's inbox but does NOT appear in taskboard.

---

## Phase 5: Human Approval Gate

**New files:**
- `internal/approval/approval.go` — `Gate` struct, `Request`, `Respond`, `ReadProposal`
- `internal/approval/approval_test.go`

**Modified files:**
- `internal/daemon/daemon.go` — watch `approvals/` dir, handle `.pending` files
- `internal/mcp/server.go` — pass `ApprovalMode` through `ServerConfig`
- `internal/mcp/architect_tools.go` — `pool_send_task` handler calls approval gate

**Approval protocol (file-based):**
1. Tool handler writes `{poolDir}/approvals/{task-id}.proposal.md` (the task details)
2. Tool handler writes `{poolDir}/approvals/{task-id}.pending` (marker)
3. Tool handler polls for `{task-id}.approved` or `{task-id}.rejected`
4. Daemon detects `.pending` via fsnotify, reads proposal
5. Daemon presents to human based on `human_inbox` config:
   - **none mode:** no gate, tool handler skips approval entirely
   - **stdout mode:** print proposal to daemon stdout, read y/n from stdin, write response file
   - **file mode** (`file:~/reviews/`): write proposal to configured directory, watch for `.approved`/`.rejected` file to appear there, copy response back to `approvals/`
6. Tool handler reads response, proceeds or returns error

**Gate struct:**
```go
type Gate struct {
    ApprovalsDir string
    PollInterval time.Duration  // 2s default
    Timeout      time.Duration  // 5min default
}
```

**Daemon additions:**
- `WithStdin(r io.Reader)` / `WithStdout(w io.Writer)` options for testability
- `handleApprovalRequest(ctx, path)` — runs in goroutine, presents to human, writes response
- **File mode handler:** parse `file:~/reviews/` from `HumanInbox`, expand `~`, write proposal to that directory, use fsnotify to watch for `.approved`/`.rejected` response, copy response file back to `{poolDir}/approvals/`
- **Presenter interface** (optional, for clean testability):
  ```go
  type Presenter interface {
      Present(ctx context.Context, proposalID, proposal string) (approved bool, err error)
  }
  ```
  Implementations: `StdoutPresenter`, `FilePresenter`. Daemon selects based on `HumanInbox` config.

---

## File Change Summary

| Phase | New Files | Modified Files |
|-------|-----------|----------------|
| 1 | `internal/frontmatter/frontmatter.go`, `internal/frontmatter/frontmatter_test.go`, `internal/contract/contract.go`, `internal/contract/store.go`, `internal/contract/contract_test.go` | `internal/mail/mail.go` |
| 2 | `internal/mcp/architect_tools.go`, `internal/mcp/architect_tools_test.go` | `internal/mcp/server.go`, `internal/mcp/config.go`, `cmd/agent-pool/main.go` |
| 3 | — | `internal/daemon/daemon.go`, `internal/mail/router.go`, `internal/daemon/daemon_test.go` |
| 4 | — | (tests only) |
| 5 | `internal/approval/approval.go`, `internal/approval/approval_test.go` | `internal/daemon/daemon.go`, `internal/mcp/server.go`, `internal/mcp/architect_tools.go` |

## Verification

After each phase, run:
```bash
make check   # vet + lint + test
```

End-to-end validation after Phase 5:
1. Create a test pool with `[architect]` section in pool.toml
2. Write a task message to postoffice addressed to architect
3. Verify daemon spawns architect with Opus model and architect MCP tools
4. Verify architect can call `pool_define_contract` and contract appears in `contracts/`
5. Verify architect can call `pool_send_task` and the approval flow triggers (stdout mode)
6. Verify `pool_amend_contract` sends notify messages to all parties
7. Verify `pool_verify_result` logs the verification entry

## Risks

- **Approval gate blocking:** Mitigated by goroutine isolation in daemon + timeout in tool handler
- **Frontmatter extraction breaking mail:** Pure refactor with identical behavior; existing tests catch regressions
- **Architect session timeout during approval wait:** Gate timeout (5min) should be shorter than session timeout
