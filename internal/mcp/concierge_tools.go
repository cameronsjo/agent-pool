package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/cameronsjo/agent-pool/internal/config"
	"github.com/cameronsjo/agent-pool/internal/expert"
	"github.com/cameronsjo/agent-pool/internal/mail"
	"github.com/cameronsjo/agent-pool/internal/taskboard"
)

const (
	defaultPollInterval = 2 * time.Second
	defaultPollTimeout  = 5 * time.Minute
)

// RegisterConciergeTools adds concierge-scope tools to the MCP server.
// These are registered in addition to the expert tools when running as
// the concierge role.
func RegisterConciergeTools(srv *server.MCPServer, cfg *ServerConfig) {
	if cfg == nil {
		return
	}

	srv.AddTool(
		mcp.NewTool("ask_expert",
			mcp.WithDescription("Send a question to an expert and wait for the response. BLOCKS until the expert completes or times out. For non-blocking dispatch, use dispatch instead."),
			mcp.WithString("expert", mcp.Required(), mcp.Description("Expert name to ask (e.g., 'auth', 'frontend')")),
			mcp.WithString("question", mcp.Required(), mcp.Description("Question body (markdown)")),
		),
		handleAskExpert(cfg),
	)

	srv.AddTool(
		mcp.NewTool("dispatch",
			mcp.WithDescription("Send a question or task to an expert without waiting. Returns a task ID immediately. Use collect to retrieve results later."),
			mcp.WithString("expert", mcp.Required(), mcp.Description("Expert name (e.g., 'auth', 'frontend')")),
			mcp.WithString("message", mcp.Required(), mcp.Description("Question or task body (markdown)")),
			mcp.WithString("type", mcp.Description("Message type: 'question' (default) or 'task'")),
		),
		handleDispatch(cfg),
	)

	srv.AddTool(
		mcp.NewTool("collect",
			mcp.WithDescription("Check dispatched tasks and return results for any that have completed. Non-blocking — returns immediately with current status."),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
			mcp.WithString("task_ids", mcp.Required(), mcp.Description("Comma-separated task IDs to check")),
		),
		handleCollect(cfg),
	)

	srv.AddTool(
		mcp.NewTool("submit_plan",
			mcp.WithDescription("Submit a plan to the architect for review and decomposition. Returns immediately with task ID — use check_status to track."),
			mcp.WithString("plan", mcp.Required(), mcp.Description("Plan body (markdown)")),
			mcp.WithString("contracts", mcp.Description("Comma-separated contract IDs to reference (optional)")),
		),
		handleSubmitPlan(cfg),
	)

	srv.AddTool(
		mcp.NewTool("check_status",
			mcp.WithDescription("Query the taskboard for task status. Returns all non-terminal tasks if no filters given."),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
			mcp.WithString("task_id", mcp.Description("Specific task ID to look up (optional)")),
			mcp.WithString("expert", mcp.Description("Filter tasks by expert name (optional)")),
			mcp.WithString("status", mcp.Description("Filter tasks by status: pending, blocked, active, completed, failed, cancelled (optional)")),
		),
		handleCheckStatus(cfg),
	)

	srv.AddTool(
		mcp.NewTool("list_experts",
			mcp.WithDescription("List available experts in the pool (pool-scoped and shared)."),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
		),
		handleListExperts(cfg),
	)
}

func handleAskExpert(cfg *ServerConfig) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		expertName := request.GetString("expert", "")
		question := request.GetString("question", "")

		if expertName == "" {
			return mcp.NewToolResultError("expert parameter is required"), nil
		}
		if question == "" {
			return mcp.NewToolResultError("question parameter is required"), nil
		}

		id := fmt.Sprintf("cq-%s-%d", expertName, time.Now().UnixNano())

		cfg.Logger.Info("Preparing to dispatch question",
			"id", id,
			"expert", expertName,
		)

		msg := &mail.Message{
			ID:        id,
			From:      "concierge",
			To:        expertName,
			Type:      mail.TypeQuestion,
			Priority:  mail.PriorityNormal,
			Timestamp: time.Now().UTC(),
			Body:      question,
		}

		if err := postMessage(cfg.PoolDir, msg); err != nil {
			cfg.Logger.Error("Failed to post question",
				"id", id,
				"expert", expertName,
				"error", err,
			)
			return mcp.NewToolResultError(err.Error()), nil
		}

		cfg.Logger.Info("Successfully dispatched question, polling for response",
			"id", id,
			"expert", expertName,
		)

		// Poll taskboard until the expert completes
		result, err := pollForCompletion(ctx, cfg, id)
		if err != nil {
			cfg.Logger.Warn("Failed to get expert response",
				"id", id,
				"expert", expertName,
				"error", err,
			)
			return mcp.NewToolResultError(fmt.Sprintf("waiting for expert: %v", err)), nil
		}

		cfg.Logger.Info("Successfully received expert response",
			"id", id,
			"expert", expertName,
		)

		return mcp.NewToolResultText(result), nil
	}
}

func handleDispatch(cfg *ServerConfig) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		expertName := request.GetString("expert", "")
		message := request.GetString("message", "")
		msgType := request.GetString("type", "question")

		if expertName == "" {
			return mcp.NewToolResultError("expert parameter is required"), nil
		}
		if message == "" {
			return mcp.NewToolResultError("message parameter is required"), nil
		}

		var mailType mail.MessageType
		switch msgType {
		case "question", "":
			mailType = mail.TypeQuestion
		case "task":
			mailType = mail.TypeTask
		default:
			return mcp.NewToolResultError(fmt.Sprintf("invalid type %q: must be 'question' or 'task'", msgType)), nil
		}

		prefix := "cq"
		if mailType == mail.TypeTask {
			prefix = "ct"
		}
		id := fmt.Sprintf("%s-%s-%d", prefix, expertName, time.Now().UnixNano())

		msg := &mail.Message{
			ID:        id,
			From:      "concierge",
			To:        expertName,
			Type:      mailType,
			Priority:  mail.PriorityNormal,
			Timestamp: time.Now().UTC(),
			Body:      message,
		}

		if err := postMessage(cfg.PoolDir, msg); err != nil {
			cfg.Logger.Error("Failed to dispatch message",
				"id", id,
				"expert", expertName,
				"error", err,
			)
			return mcp.NewToolResultError(err.Error()), nil
		}

		cfg.Logger.Info("Successfully dispatched message",
			"id", id,
			"expert", expertName,
			"type", msgType,
		)

		return mcp.NewToolResultText(fmt.Sprintf("dispatched (id: %s)", id)), nil
	}
}

// collectResult represents one task's status in a collect response.
type collectResult struct {
	ID       string  `json:"id"`
	Expert   string  `json:"expert"`
	Status   string  `json:"status"`
	Duration string  `json:"duration,omitempty"`
	Result   string  `json:"result,omitempty"`
	Error    string  `json:"error,omitempty"`
}

func handleCollect(cfg *ServerConfig) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		idsStr := request.GetString("task_ids", "")
		if idsStr == "" {
			return mcp.NewToolResultError("task_ids parameter is required"), nil
		}

		var taskIDs []string
		for _, id := range strings.Split(idsStr, ",") {
			id = strings.TrimSpace(id)
			if id != "" {
				taskIDs = append(taskIDs, id)
			}
		}

		boardPath := filepath.Join(cfg.PoolDir, "taskboard.json")
		board, err := taskboard.Load(boardPath)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("loading taskboard: %v", err)), nil
		}

		var results []collectResult
		for _, id := range taskIDs {
			task, found := board.Get(id)
			if !found {
				results = append(results, collectResult{
					ID:     id,
					Status: "not_found",
				})
				continue
			}

			cr := collectResult{
				ID:     id,
				Expert: task.Expert,
				Status: string(task.Status),
			}

			if task.StartedAt != nil && task.CompletedAt != nil {
				cr.Duration = task.CompletedAt.Sub(*task.StartedAt).Round(time.Second).String()
			} else if task.StartedAt != nil {
				cr.Duration = time.Since(*task.StartedAt).Round(time.Second).String()
			}

			if task.Status == taskboard.StatusCompleted {
				result, readErr := readExpertResult(cfg.PoolDir, task.Expert, id)
				if readErr != nil {
					cr.Error = readErr.Error()
				} else {
					cr.Result = result
				}
			} else if task.Status == taskboard.StatusFailed {
				cr.Error = "expert session failed"
				if task.ExitCode != nil {
					cr.Error = fmt.Sprintf("expert session failed (exit code: %d)", *task.ExitCode)
				}
			} else if task.Status == taskboard.StatusCancelled {
				cr.Error = task.CancelNote
			}

			results = append(results, cr)
		}

		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshaling results: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

// pollForCompletion blocks until a task reaches a terminal status, then reads
// the expert's session log and extracts the result. Uses task.Expert from the
// taskboard (not the original parameter) so it stays correct if routing changes.
func pollForCompletion(ctx context.Context, cfg *ServerConfig, taskID string) (string, error) {
	boardPath := filepath.Join(cfg.PoolDir, "taskboard.json")

	deadlineCtx, cancel := context.WithTimeout(ctx, defaultPollTimeout)
	defer cancel()

	ticker := time.NewTicker(defaultPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-deadlineCtx.Done():
			cfg.Logger.Warn("Timed out waiting for expert response",
				"task_id", taskID,
				"timeout", defaultPollTimeout,
			)
			return "", fmt.Errorf("timed out after %v waiting for task %s", defaultPollTimeout, taskID)

		case <-ticker.C:
			board, err := taskboard.Load(boardPath)
			if err != nil {
				// Load returns an empty board for file-not-found; errors here
				// indicate permission issues or corrupt JSON — stop polling.
				return "", fmt.Errorf("loading taskboard: %w", err)
			}

			task, found := board.Get(taskID)
			if !found {
				// Task not yet registered by daemon
				continue
			}

			switch task.Status {
			case taskboard.StatusCompleted:
				return readExpertResult(cfg.PoolDir, task.Expert, taskID)
			case taskboard.StatusFailed:
				exitInfo := ""
				if task.ExitCode != nil {
					exitInfo = fmt.Sprintf(" (exit code: %d)", *task.ExitCode)
				}
				cfg.Logger.Warn("Expert task failed",
					"task_id", taskID,
					"expert", task.Expert,
					"exit_code", task.ExitCode,
				)
				return "", fmt.Errorf("expert %q failed task %s%s", task.Expert, taskID, exitInfo)
			case taskboard.StatusCancelled:
				cfg.Logger.Warn("Expert task was cancelled",
					"task_id", taskID,
					"expert", task.Expert,
					"cancel_note", task.CancelNote,
				)
				return "", fmt.Errorf("task %s was cancelled: %s", taskID, task.CancelNote)
			}
			// Still pending/blocked/active — keep polling
		}
	}
}

// readExpertResult reads the expert's session log and extracts the full result.
func readExpertResult(poolDir, expertName, taskID string) (string, error) {
	expertDir := mail.ResolveExpertDir(poolDir, expertName)

	logData, err := expert.ReadLog(expertDir, taskID)
	if err != nil {
		return "", fmt.Errorf("reading expert log: %w", err)
	}

	result := expert.ExtractResult(logData)
	return result, nil
}

func handleSubmitPlan(cfg *ServerConfig) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		plan := request.GetString("plan", "")
		contractsStr := request.GetString("contracts", "")

		if plan == "" {
			return mcp.NewToolResultError("plan parameter is required"), nil
		}

		var contracts []string
		if contractsStr != "" {
			for _, c := range strings.Split(contractsStr, ",") {
				c = strings.TrimSpace(c)
				if c != "" {
					contracts = append(contracts, c)
				}
			}
		}

		id := fmt.Sprintf("cp-%d", time.Now().UnixNano())

		cfg.Logger.Info("Preparing to submit plan to architect",
			"id", id,
			"contract_count", len(contracts),
		)

		msg := &mail.Message{
			ID:        id,
			From:      "concierge",
			To:        "architect",
			Type:      mail.TypeTask,
			Contracts: contracts,
			Priority:  mail.PriorityNormal,
			Timestamp: time.Now().UTC(),
			Body:      plan,
		}

		if err := postMessage(cfg.PoolDir, msg); err != nil {
			cfg.Logger.Error("Failed to post plan",
				"id", id,
				"error", err,
			)
			return mcp.NewToolResultError(err.Error()), nil
		}

		cfg.Logger.Info("Successfully submitted plan to architect",
			"id", id,
			"contracts", contracts,
		)

		return mcp.NewToolResultText(fmt.Sprintf("plan submitted (id: %s)", id)), nil
	}
}

func handleCheckStatus(cfg *ServerConfig) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		taskID := request.GetString("task_id", "")
		expertFilter := request.GetString("expert", "")
		statusFilter := request.GetString("status", "")

		boardPath := filepath.Join(cfg.PoolDir, "taskboard.json")
		board, err := taskboard.Load(boardPath)
		if err != nil {
			cfg.Logger.Error("Failed to load taskboard",
				"error", err,
			)
			return mcp.NewToolResultError(fmt.Sprintf("loading taskboard: %v", err)), nil
		}

		// Single task lookup
		if taskID != "" {
			task, found := board.Get(taskID)
			if !found {
				return mcp.NewToolResultError(fmt.Sprintf("task %q not found", taskID)), nil
			}
			data, err := json.MarshalIndent(task, "", "  ")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("marshaling task: %v", err)), nil
			}
			return mcp.NewToolResultText(string(data)), nil
		}

		// Filtered listing
		var results []*taskboard.Task
		for _, task := range board.Tasks {
			if expertFilter != "" && task.Expert != expertFilter {
				continue
			}
			if statusFilter != "" && string(task.Status) != statusFilter {
				continue
			}
			// Default: exclude terminal tasks when no filters given
			if expertFilter == "" && statusFilter == "" && isTerminal(task.Status) {
				continue
			}
			results = append(results, task)
		}

		if len(results) == 0 {
			return mcp.NewToolResultText("no matching tasks"), nil
		}

		// Sort by creation time with ID tiebreaker for stable output
		sort.Slice(results, func(i, j int) bool {
			if results[i].CreatedAt.Equal(results[j].CreatedAt) {
				return results[i].ID < results[j].ID
			}
			return results[i].CreatedAt.Before(results[j].CreatedAt)
		})

		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshaling results: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

func isTerminal(status taskboard.Status) bool {
	switch status {
	case taskboard.StatusCompleted, taskboard.StatusFailed, taskboard.StatusCancelled:
		return true
	}
	return false
}

func handleListExperts(cfg *ServerConfig) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		poolCfg, err := config.LoadPool(cfg.PoolDir)
		if err != nil {
			cfg.Logger.Error("Failed to load pool config",
				"error", err,
			)
			return mcp.NewToolResultError(fmt.Sprintf("loading pool config: %v", err)), nil
		}

		var poolExperts []string
		for name := range poolCfg.Experts {
			poolExperts = append(poolExperts, name)
		}
		sort.Strings(poolExperts)

		result := map[string][]string{
			"experts":        poolExperts,
			"pool_experts":   poolExperts, // compatibility alias (deprecated)
			"shared_experts": poolCfg.Shared.Include,
		}

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshaling result: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}
