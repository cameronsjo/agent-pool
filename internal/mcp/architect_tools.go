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

	"git.sjo.lol/cameron/agent-pool/internal/contract"
	"git.sjo.lol/cameron/agent-pool/internal/mail"
)

// RegisterArchitectTools adds architect-scope tools to the MCP server.
// These are registered in addition to the expert tools when running as
// the architect role.
func RegisterArchitectTools(srv *server.MCPServer, cfg *ServerConfig) {
	if cfg == nil {
		return
	}

	store := contract.NewStore(cfg.PoolDir)

	srv.AddTool(
		mcp.NewTool("pool_define_contract",
			mcp.WithDescription("Define a new contract between experts. Creates a versioned interface specification."),
			mcp.WithString("id", mcp.Required(), mcp.Description("Contract ID (must be filename-safe)")),
			mcp.WithString("between", mcp.Required(), mcp.Description("Comma-separated list of expert names (at least 2)")),
			mcp.WithString("body", mcp.Required(), mcp.Description("Contract body (markdown with interface specs and constraints)")),
		),
		handleDefineContract(store),
	)

	srv.AddTool(
		mcp.NewTool("pool_send_task",
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
		mcp.NewTool("pool_verify_result",
			mcp.WithDescription("Log a verification result for a task against a contract specification."),
			mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID being verified")),
			mcp.WithString("contract_id", mcp.Required(), mcp.Description("Contract ID verified against")),
			mcp.WithString("status", mcp.Required(), mcp.Description("Verification status: pass, fail, or partial")),
			mcp.WithString("notes", mcp.Required(), mcp.Description("Verification notes and findings")),
		),
		handleVerifyResult(cfg, store),
	)

	srv.AddTool(
		mcp.NewTool("pool_amend_contract",
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
			priority = mail.Priority(priorityStr)
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

		composed, err := mail.Compose(msg)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("composing message: %v", err)), nil
		}

		postoffice := filepath.Join(cfg.PoolDir, "postoffice")
		path := filepath.Join(postoffice, id+".md")

		if err := os.WriteFile(path, []byte(composed), 0o644); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("writing to postoffice: %v", err)), nil
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
		if contractID == "" {
			return mcp.NewToolResultError("contract_id parameter is required"), nil
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
		postoffice := filepath.Join(cfg.PoolDir, "postoffice")
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

			composed, composeErr := mail.Compose(notifyMsg)
			if composeErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("composing notify for %s: %v", party, composeErr)), nil
			}

			path := filepath.Join(postoffice, notifyMsg.ID+".md")
			if writeErr := os.WriteFile(path, []byte(composed), 0o644); writeErr != nil {
				return mcp.NewToolResultError(fmt.Sprintf("writing notify for %s: %v", party, writeErr)), nil
			}
		}

		return mcp.NewToolResultText(fmt.Sprintf("contract %s amended to v%d, notified: %s",
			id, amended.Version, strings.Join(amended.Between, ", "))), nil
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
