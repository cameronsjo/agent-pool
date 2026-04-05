package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/cameronsjo/agent-pool/internal/approval"
	"github.com/cameronsjo/agent-pool/internal/contract"
	"github.com/cameronsjo/agent-pool/internal/mail"
)

// RegisterArchitectTools adds architect-scope tools to the MCP server.
// These are registered in addition to the expert tools when running as
// the architect role.
func RegisterArchitectTools(srv *server.MCPServer, cfg *ServerConfig) {
	if cfg == nil {
		return
	}

	store := contract.NewStore(cfg.PoolDir).WithLogger(cfg.Logger)

	srv.AddTool(
		mcp.NewTool("define_contract",
			mcp.WithDescription("Define a new contract between experts. Creates a versioned interface specification."),
			mcp.WithString("id", mcp.Required(), mcp.Description("Contract ID (must be filename-safe)")),
			mcp.WithString("between", mcp.Required(), mcp.Description("Comma-separated list of expert names (at least 2)")),
			mcp.WithString("body", mcp.Required(), mcp.Description("Contract body (markdown with interface specs and constraints)")),
		),
		handleDefineContract(store),
	)

	srv.AddTool(
		mcp.NewTool("send_task",
			mcp.WithDescription("Delegate a task to an expert via the postoffice. References contracts the expert must follow."),
			mcp.WithString("to", mcp.Required(), mcp.Description("Recipient expert name")),
			mcp.WithString("body", mcp.Required(), mcp.Description("Task description (markdown)")),
			mcp.WithString("id", mcp.Required(), mcp.Description("Task message ID (must be filename-safe)")),
			mcp.WithString("contracts", mcp.Description("Comma-separated contract IDs to reference (optional)")),
			mcp.WithString("depends_on", mcp.Description("Comma-separated task IDs this task depends on (optional)")),
			mcp.WithString("priority", mcp.Description("Priority: low, normal (default), high, urgent")),
		),
		handleSendTask(cfg),
	)

	srv.AddTool(
		mcp.NewTool("verify_result",
			mcp.WithDescription("Log a verification result for a task against a contract specification."),
			mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID being verified")),
			mcp.WithString("contract_id", mcp.Required(), mcp.Description("Contract ID verified against")),
			mcp.WithString("status", mcp.Required(), mcp.Description("Verification status: pass, fail, or partial")),
			mcp.WithString("notes", mcp.Required(), mcp.Description("Verification notes and findings")),
		),
		handleVerifyResult(cfg, store),
	)

	srv.AddTool(
		mcp.NewTool("amend_contract",
			mcp.WithDescription("Amend an existing contract. Increments version and notifies all parties."),
			mcp.WithString("id", mcp.Required(), mcp.Description("Contract ID to amend")),
			mcp.WithString("body", mcp.Required(), mcp.Description("New contract body (markdown)")),
		),
		handleAmendContract(cfg, store),
	)
}

func handleDefineContract(store *contract.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := request.GetString("id", "")
		between := request.GetString("between", "")
		body := request.GetString("body", "")

		if id == "" {
			return mcp.NewToolResultError("id parameter is required"), nil
		}
		if id != filepath.Base(id) || id == "." || id == ".." {
			return mcp.NewToolResultError(fmt.Sprintf("invalid contract ID %q: must be a simple filename", id)), nil
		}
		if between == "" {
			return mcp.NewToolResultError("between parameter is required"), nil
		}
		if body == "" {
			return mcp.NewToolResultError("body parameter is required"), nil
		}

		parties := splitCSV(between)
		if len(parties) < 2 {
			return mcp.NewToolResultError("between must list at least 2 expert names"), nil
		}

		c := &contract.Contract{
			ID:        id,
			Type:      "contract",
			DefinedBy: "architect",
			Between:   parties,
			Version:   1,
			Timestamp: time.Now().UTC(),
			Body:      body,
		}

		if err := store.Save(c); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("saving contract: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("contract %s defined (v1, between: %s)", id, between)), nil
	}
}

func handleSendTask(cfg *ServerConfig) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		to := request.GetString("to", "")
		body := request.GetString("body", "")
		id := request.GetString("id", "")
		contractsStr := request.GetString("contracts", "")
		dependsOnStr := request.GetString("depends_on", "")
		priorityStr := request.GetString("priority", "")

		if to == "" {
			return mcp.NewToolResultError("to parameter is required"), nil
		}
		if body == "" {
			return mcp.NewToolResultError("body parameter is required"), nil
		}
		if id == "" {
			return mcp.NewToolResultError("id parameter is required"), nil
		}
		if id != filepath.Base(id) || id == "." || id == ".." {
			return mcp.NewToolResultError(fmt.Sprintf("invalid message ID %q: must be a simple filename", id)), nil
		}

		priority := mail.PriorityNormal
		if priorityStr != "" {
			switch mail.Priority(priorityStr) {
			case mail.PriorityLow, mail.PriorityNormal, mail.PriorityHigh, mail.PriorityUrgent:
				priority = mail.Priority(priorityStr)
			default:
				return mcp.NewToolResultError(fmt.Sprintf("invalid priority %q: must be low, normal, high, or urgent", priorityStr)), nil
			}
		}

		var contracts []string
		if contractsStr != "" {
			contracts = splitCSV(contractsStr)
		}

		var dependsOn []string
		if dependsOnStr != "" {
			dependsOn = splitCSV(dependsOnStr)
		}

		msg := &mail.Message{
			ID:        id,
			From:      "architect",
			To:        to,
			Type:      mail.TypeTask,
			Contracts: contracts,
			Priority:  priority,
			DependsOn: dependsOn,
			Timestamp: time.Now().UTC(),
			Body:      body,
		}

		// Approval gate: block on human approval if required
		if shouldRequireApproval(cfg.ApprovalMode) {
			gate := approval.DefaultGate(cfg.PoolDir)
			gate.Logger = cfg.Logger
			proposalSummary := fmt.Sprintf("Task: %s\nTo: %s\nPriority: %s\nContracts: %s\n\n%s",
				id, to, priority, strings.Join(contracts, ", "), body)
			if gateErr := gate.Request(ctx, id, proposalSummary); gateErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("approval gate: %v", gateErr)), nil
			}
		}

		if err := postMessage(cfg.PoolDir, msg); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("task %s sent to %s", id, to)), nil
	}
}

func handleVerifyResult(cfg *ServerConfig, store *contract.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		taskID := request.GetString("task_id", "")
		contractID := request.GetString("contract_id", "")
		status := request.GetString("status", "")
		notes := request.GetString("notes", "")

		if taskID == "" {
			return mcp.NewToolResultError("task_id parameter is required"), nil
		}
		if taskID != filepath.Base(taskID) || taskID == "." || taskID == ".." {
			return mcp.NewToolResultError(fmt.Sprintf("invalid task_id %q: must be a simple filename", taskID)), nil
		}
		if contractID == "" {
			return mcp.NewToolResultError("contract_id parameter is required"), nil
		}
		if contractID != filepath.Base(contractID) || contractID == "." || contractID == ".." {
			return mcp.NewToolResultError(fmt.Sprintf("invalid contract_id %q: must be a simple filename", contractID)), nil
		}
		if status == "" {
			return mcp.NewToolResultError("status parameter is required"), nil
		}
		if notes == "" {
			return mcp.NewToolResultError("notes parameter is required"), nil
		}

		switch status {
		case "pass", "fail", "partial":
			// valid
		default:
			return mcp.NewToolResultError(fmt.Sprintf("invalid status %q: must be pass, fail, or partial", status)), nil
		}

		// Verify contract exists
		if _, err := store.Load(contractID); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("contract %q not found: %v", contractID, err)), nil
		}

		// Write verification entry to architect's verification log
		architectDir := filepath.Join(cfg.PoolDir, "architect")
		verifyDir := filepath.Join(architectDir, "verifications")
		if err := os.MkdirAll(verifyDir, 0o755); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("creating verifications dir: %v", err)), nil
		}

		entry := fmt.Sprintf("---\ntask: %s\ncontract: %s\nstatus: %s\ntimestamp: %s\n---\n\n%s\n",
			taskID, contractID, status, time.Now().UTC().Format(time.RFC3339), notes)

		filename := fmt.Sprintf("%s_%s.md", taskID, contractID)
		path := filepath.Join(verifyDir, filename)

		if err := os.WriteFile(path, []byte(entry), 0o644); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("writing verification: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("verification logged: task %s against %s = %s", taskID, contractID, status)), nil
	}
}

func handleAmendContract(cfg *ServerConfig, store *contract.Store) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id := request.GetString("id", "")
		body := request.GetString("body", "")

		if id == "" {
			return mcp.NewToolResultError("id parameter is required"), nil
		}
		if body == "" {
			return mcp.NewToolResultError("body parameter is required"), nil
		}

		amended, err := store.Amend(id, body)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("amending contract: %v", err)), nil
		}

		// Fan-out: send notify messages to all parties
		for _, party := range amended.Between {
			notifyMsg := &mail.Message{
				ID:        fmt.Sprintf("notify-%s-v%d-%s", id, amended.Version, party),
				From:      "architect",
				To:        party,
				Type:      mail.TypeNotify,
				Contracts: []string{id},
				Priority:  mail.PriorityNormal,
				Timestamp: time.Now().UTC(),
				Body:      fmt.Sprintf("Contract %s has been amended to version %d. Please review the updated specification.", id, amended.Version),
			}

			if err := postMessage(cfg.PoolDir, notifyMsg); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("notify for %s: %v", party, err)), nil
			}
		}

		return mcp.NewToolResultText(fmt.Sprintf("contract %s amended to v%d, notified: %s",
			id, amended.Version, strings.Join(amended.Between, ", "))), nil
	}
}

// shouldRequireApproval returns whether the given approval mode requires
// human approval before task dispatch.
func shouldRequireApproval(mode string) bool {
	switch mode {
	case "none", "":
		return false
	case "decomposition", "all":
		return true
	default:
		return true // safe default
	}
}

// splitCSV splits a comma-separated string into trimmed, non-empty parts.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
