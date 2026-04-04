package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"git.sjo.lol/cameron/agent-pool/internal/expert"
	"git.sjo.lol/cameron/agent-pool/internal/mail"
)

// RegisterExpertTools adds all expert-scope tools to the MCP server.
func RegisterExpertTools(srv *server.MCPServer, cfg *ServerConfig) {
	if cfg == nil {
		return
	}
	expertDir := mail.ResolveExpertDir(cfg.PoolDir, cfg.ExpertName)

	srv.AddTool(
		mcp.NewTool("pool_read_state",
			mcp.WithDescription("Read current expert state files (identity.md, state.md, errors.md)"),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
		),
		handleReadState(expertDir),
	)

	srv.AddTool(
		mcp.NewTool("pool_update_state",
			mcp.WithDescription("Update the expert's working memory (state.md). Content must be non-empty and under 50KB."),
			mcp.WithString("content", mcp.Required(), mcp.Description("New state.md content")),
		),
		handleUpdateState(expertDir),
	)

	srv.AddTool(
		mcp.NewTool("pool_append_error",
			mcp.WithDescription("Append a structured error entry to the expert's error log (errors.md). Each entry is timestamped."),
			mcp.WithString("entry", mcp.Required(), mcp.Description("Error description to append")),
		),
		handleAppendError(expertDir),
	)

	srv.AddTool(
		mcp.NewTool("pool_send_response",
			mcp.WithDescription("Send a response message to another agent via the postoffice."),
			mcp.WithString("to", mcp.Required(), mcp.Description("Recipient agent name")),
			mcp.WithString("body", mcp.Required(), mcp.Description("Response body (markdown)")),
			mcp.WithString("id", mcp.Required(), mcp.Description("Message ID (must be filename-safe)")),
		),
		handleSendResponse(cfg),
	)

	srv.AddTool(
		mcp.NewTool("pool_recall",
			mcp.WithDescription("Read a prior task log by its task ID."),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
			mcp.WithString("task_id", mcp.Required(), mcp.Description("Task ID to recall")),
		),
		handleRecall(expertDir),
	)

	srv.AddTool(
		mcp.NewTool("pool_search_index",
			mcp.WithDescription("Search the task log index for relevant prior tasks. Case-insensitive substring match."),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
			mcp.WithString("query", mcp.Required(), mcp.Description("Search query")),
		),
		handleSearchIndex(expertDir),
	)
}

func handleReadState(expertDir string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		identity, state, errors, err := expert.ReadState(expertDir)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("reading state: %v", err)), nil
		}

		result := map[string]string{
			"identity": identity,
			"state":    state,
			"errors":   errors,
		}

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshaling result: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

func handleUpdateState(expertDir string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		content := request.GetString("content", "")
		if content == "" {
			return mcp.NewToolResultError("content parameter is required"), nil
		}

		if err := expert.WriteState(expertDir, content); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("writing state: %v", err)), nil
		}

		return mcp.NewToolResultText("state.md updated"), nil
	}
}

func handleAppendError(expertDir string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		entry := request.GetString("entry", "")
		if entry == "" {
			return mcp.NewToolResultError("entry parameter is required"), nil
		}

		if err := expert.AppendError(expertDir, entry); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("appending error: %v", err)), nil
		}

		return mcp.NewToolResultText("error entry appended to errors.md"), nil
	}
}

func handleSendResponse(cfg *ServerConfig) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		to := request.GetString("to", "")
		body := request.GetString("body", "")
		id := request.GetString("id", "")

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

		msg := &mail.Message{
			ID:        id,
			From:      cfg.ExpertName,
			To:        to,
			Type:      mail.TypeResponse,
			Priority:  mail.PriorityNormal,
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

		return mcp.NewToolResultText(fmt.Sprintf("response sent to %s (id: %s)", to, id)), nil
	}
}

func handleRecall(expertDir string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		taskID := request.GetString("task_id", "")
		if taskID == "" {
			return mcp.NewToolResultError("task_id parameter is required"), nil
		}

		data, err := expert.ReadLog(expertDir, taskID)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("reading log: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

func handleSearchIndex(expertDir string) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := request.GetString("query", "")
		if query == "" {
			return mcp.NewToolResultError("query parameter is required"), nil
		}

		matches, err := expert.SearchIndex(expertDir, query)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("searching index: %v", err)), nil
		}

		if len(matches) == 0 {
			return mcp.NewToolResultText("no matching tasks found"), nil
		}

		return mcp.NewToolResultText(strings.Join(matches, "\n")), nil
	}
}

func boolPtr(b bool) *bool {
	return &b
}
