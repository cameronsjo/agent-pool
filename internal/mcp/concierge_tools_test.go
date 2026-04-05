// Concierge tools coverage matrix:
//
// RegisterConciergeTools (Classification: INTEGRATION)
//   [x] Happy: all 4 concierge tools + 6 expert tools registered (TestConciergeTools_Registration)
//
//ask_expert (Classification: FILESYSTEM I/O + CONCURRENCY)
//   [x] Happy: question dispatched, polls taskboard, returns result (TestAskExpert_Happy)
//   [x] Error: missing params (TestAskExpert_MissingParams)
//   [x] Error: expert task fails (TestAskExpert_ExpertFails)
//   [x] Error: task cancelled with note (TestAskExpert_TaskCancelled)
//   [x] Error: context timeout (TestAskExpert_Timeout)
//
//submit_plan (Classification: FILESYSTEM I/O)
//   [x] Happy: plan message in postoffice, returns task ID (TestSubmitPlan_Happy)
//   [x] Happy: plan with contracts (TestSubmitPlan_WithContracts)
//   [x] Error: missing plan param (TestSubmitPlan_MissingPlan)
//
//check_status (Classification: FILESYSTEM I/O)
//   [x] Happy: single task lookup (TestCheckStatus_SingleTask)
//   [x] Happy: filter by expert (TestCheckStatus_FilterByExpert)
//   [x] Happy: filter by status (TestCheckStatus_FilterByStatus)
//   [x] Happy: default excludes terminal (TestCheckStatus_DefaultExcludesTerminal)
//   [x] Error: task not found (TestCheckStatus_NotFound)
//
//list_experts (Classification: FILESYSTEM I/O)
//   [x] Happy: lists pool and shared experts (TestListExperts_Happy)
//   [x] Error: missing pool.toml (TestListExperts_MissingConfig)

package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cameronsjo/agent-pool/internal/expert"
	"github.com/cameronsjo/agent-pool/internal/mail"
	"github.com/cameronsjo/agent-pool/internal/taskboard"

	"github.com/mark3labs/mcp-go/server"
)

// setupConciergePool creates a pool directory with concierge-relevant dirs.
func setupConciergePool(t *testing.T) string {
	t.Helper()
	return makePoolDirs(t,
		"postoffice", "concierge/inbox", "architect/inbox",
		"experts/auth/inbox", "experts/auth/logs",
		"experts/frontend/inbox", "experts/frontend/logs",
	)
}

// buildConciergeTestServer creates an MCP server with expert + concierge tools.
func buildConciergeTestServer(t *testing.T, poolDir string) *server.MCPServer {
	t.Helper()
	return buildMCPTestServer(t, poolDir, "concierge", "concierge")
}

// fakeStreamJSON builds stream-json output containing a result message.
func fakeStreamJSON(result string) []byte {
	b, _ := json.Marshal(result)
	msg := fmt.Sprintf(`{"type":"result","result":%s}`, string(b))
	return []byte(msg + "\n")
}

// --- Registration ---

func TestConciergeTools_Registration(t *testing.T) {
	poolDir := setupConciergePool(t)
	srv := buildConciergeTestServer(t, poolDir)

	tools := listToolNames(t, srv)

	expected := []string{
		// Concierge tools
		"ask_expert", "submit_plan",
		"check_status", "list_experts",
		// Expert tools (inherited)
		"read_state", "update_state",
		"append_error", "send_response",
		"recall", "search_index",
	}
	for _, name := range expected {
		if !tools[name] {
			t.Errorf("missing tool: %s", name)
		}
	}

	// Should NOT have architect tools
	architectOnly := []string{"define_contract", "send_task", "verify_result", "amend_contract"}
	for _, name := range architectOnly {
		if tools[name] {
			t.Errorf("unexpected architect tool registered for concierge: %s", name)
		}
	}
}

// --- pool_ask_expert ---

func TestAskExpert_Happy(t *testing.T) {
	poolDir := setupConciergePool(t)
	srv := buildConciergeTestServer(t, poolDir)

	expertResponse := "Token refresh uses HTTP-only cookies to store the refresh token. The access token expires in 15 minutes."

	// Goroutine simulates daemon: watches postoffice, creates completed task + log
	go func() {
		postoffice := filepath.Join(poolDir, "postoffice")
		var msgID string

		// Poll for the question message to appear
		for i := 0; i < 50; i++ {
			time.Sleep(50 * time.Millisecond)
			files, _ := filepath.Glob(filepath.Join(postoffice, "cq-auth-*.md"))
			if len(files) > 0 {
				msg, err := mail.ParseFile(files[0])
				if err != nil {
					continue
				}
				msgID = msg.ID
				break
			}
		}
		if msgID == "" {
			return // test will fail on timeout
		}

		// Write expert log
		expertDir := filepath.Join(poolDir, "experts", "auth")
		expert.WriteLog(expertDir, msgID, fakeStreamJSON(expertResponse))

		// Create completed taskboard entry
		now := time.Now()
		exitCode := 0
		board := &taskboard.Board{
			Version: 1,
			Tasks: map[string]*taskboard.Task{
				msgID: {
					ID:          msgID,
					Status:      taskboard.StatusCompleted,
					Expert:      "auth",
					From:        "concierge",
					Type:        "question",
					Priority:    "normal",
					CreatedAt:   now.Add(-2 * time.Second),
					CompletedAt: &now,
					ExitCode:    &exitCode,
				},
			},
		}
		board.Save(filepath.Join(poolDir, "taskboard.json"))
	}()

	result := callTool(t, srv, "ask_expert", map[string]any{
		"expert":   "auth",
		"question": "How does token refresh work?",
	})

	text := resultText(t, result)
	if !strings.Contains(text, "Token refresh") {
		t.Errorf("result %q does not contain expected response", text)
	}

	// Verify message was written to postoffice
	files, _ := filepath.Glob(filepath.Join(poolDir, "postoffice", "cq-auth-*.md"))
	if len(files) == 0 {
		t.Error("no question message found in postoffice")
	}
}

func TestAskExpert_MissingParams(t *testing.T) {
	poolDir := setupConciergePool(t)
	srv := buildConciergeTestServer(t, poolDir)

	t.Run("missing_expert", func(t *testing.T) {
		result := callTool(t, srv, "ask_expert", map[string]any{
			"question": "How does auth work?",
		})
		if !result.IsError {
			t.Error("expected error for missing expert")
		}
	})

	t.Run("missing_question", func(t *testing.T) {
		result := callTool(t, srv, "ask_expert", map[string]any{
			"expert": "auth",
		})
		if !result.IsError {
			t.Error("expected error for missing question")
		}
	})
}

func TestAskExpert_ExpertFails(t *testing.T) {
	poolDir := setupConciergePool(t)
	srv := buildConciergeTestServer(t, poolDir)

	// Goroutine creates a failed task entry
	go func() {
		postoffice := filepath.Join(poolDir, "postoffice")
		var msgID string

		for i := 0; i < 50; i++ {
			time.Sleep(50 * time.Millisecond)
			files, _ := filepath.Glob(filepath.Join(postoffice, "cq-auth-*.md"))
			if len(files) > 0 {
				msg, _ := mail.ParseFile(files[0])
				if msg != nil {
					msgID = msg.ID
					break
				}
			}
		}
		if msgID == "" {
			return
		}

		now := time.Now()
		exitCode := 1
		board := &taskboard.Board{
			Version: 1,
			Tasks: map[string]*taskboard.Task{
				msgID: {
					ID:          msgID,
					Status:      taskboard.StatusFailed,
					Expert:      "auth",
					From:        "concierge",
					Type:        "question",
					CreatedAt:   now.Add(-2 * time.Second),
					CompletedAt: &now,
					ExitCode:    &exitCode,
				},
			},
		}
		board.Save(filepath.Join(poolDir, "taskboard.json"))
	}()

	result := callTool(t, srv, "ask_expert", map[string]any{
		"expert":   "auth",
		"question": "How does auth work?",
	})

	if !result.IsError {
		t.Error("expected error for failed expert task")
	}
	text := resultText(t, result)
	if !strings.Contains(text, "failed") {
		t.Errorf("error %q should mention failure", text)
	}
}

func TestAskExpert_TaskCancelled(t *testing.T) {
	poolDir := setupConciergePool(t)
	srv := buildConciergeTestServer(t, poolDir)

	// Goroutine creates a cancelled task entry
	go func() {
		postoffice := filepath.Join(poolDir, "postoffice")
		var msgID string

		for i := 0; i < 50; i++ {
			time.Sleep(50 * time.Millisecond)
			files, _ := filepath.Glob(filepath.Join(postoffice, "cq-auth-*.md"))
			if len(files) > 0 {
				msg, _ := mail.ParseFile(files[0])
				if msg != nil {
					msgID = msg.ID
					break
				}
			}
		}
		if msgID == "" {
			return
		}

		now := time.Now()
		board := &taskboard.Board{
			Version: 1,
			Tasks: map[string]*taskboard.Task{
				msgID: {
					ID:          msgID,
					Status:      taskboard.StatusCancelled,
					Expert:      "auth",
					From:        "concierge",
					Type:        "question",
					CreatedAt:   now.Add(-2 * time.Second),
					CompletedAt: &now,
					CancelNote:  "superseded by higher priority task",
				},
			},
		}
		board.Save(filepath.Join(poolDir, "taskboard.json"))
	}()

	result := callTool(t, srv, "ask_expert", map[string]any{
		"expert":   "auth",
		"question": "How does auth work?",
	})

	if !result.IsError {
		t.Error("expected error for cancelled task")
	}
	text := resultText(t, result)
	if !strings.Contains(text, "cancelled") {
		t.Errorf("error %q should mention cancellation", text)
	}
	if !strings.Contains(text, "superseded") {
		t.Errorf("error %q should include cancel note", text)
	}
}

func TestAskExpert_Timeout(t *testing.T) {
	poolDir := setupConciergePool(t)
	srv := buildConciergeTestServer(t, poolDir)

	// Use a short-deadline context so pollForCompletion times out quickly.
	// The task stays pending forever (no goroutine to complete it).
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	result := callToolWithContext(t, ctx, srv, "ask_expert", map[string]any{
		"expert":   "auth",
		"question": "This will time out",
	})

	if !result.IsError {
		t.Error("expected error for timeout")
	}
	text := resultText(t, result)
	if !strings.Contains(text, "timed out") {
		t.Errorf("error %q should mention timeout", text)
	}
}

// --- pool_submit_plan ---

func TestSubmitPlan_Happy(t *testing.T) {
	poolDir := setupConciergePool(t)
	srv := buildConciergeTestServer(t, poolDir)

	result := callTool(t, srv, "submit_plan", map[string]any{
		"plan": "## OAuth Login Flow\n\nImplement Google OAuth with PKCE.",
	})

	text := resultText(t, result)
	if !strings.Contains(text, "plan submitted") {
		t.Errorf("result %q does not contain 'plan submitted'", text)
	}
	if !strings.Contains(text, "cp-") {
		t.Errorf("result %q does not contain task ID prefix", text)
	}

	// Verify message in postoffice
	files, _ := filepath.Glob(filepath.Join(poolDir, "postoffice", "cp-*.md"))
	if len(files) != 1 {
		t.Fatalf("expected 1 plan message, got %d", len(files))
	}

	msg, err := mail.ParseFile(files[0])
	if err != nil {
		t.Fatalf("parsing plan message: %v", err)
	}
	if msg.To != "architect" {
		t.Errorf("to = %q, want 'architect'", msg.To)
	}
	if msg.From != "concierge" {
		t.Errorf("from = %q, want 'concierge'", msg.From)
	}
	if msg.Type != mail.TypeTask {
		t.Errorf("type = %q, want 'task'", msg.Type)
	}
}

func TestSubmitPlan_WithContracts(t *testing.T) {
	poolDir := setupConciergePool(t)
	srv := buildConciergeTestServer(t, poolDir)

	result := callTool(t, srv, "submit_plan", map[string]any{
		"plan":      "Implement the auth flow",
		"contracts": "auth-api-v1, session-store-v2",
	})

	text := resultText(t, result)
	if !strings.Contains(text, "plan submitted") {
		t.Errorf("result %q does not contain 'plan submitted'", text)
	}

	// Verify contracts in message
	files, _ := filepath.Glob(filepath.Join(poolDir, "postoffice", "cp-*.md"))
	if len(files) != 1 {
		t.Fatalf("expected 1 plan message, got %d", len(files))
	}

	msg, err := mail.ParseFile(files[0])
	if err != nil {
		t.Fatalf("parsing plan message: %v", err)
	}
	if len(msg.Contracts) != 2 {
		t.Fatalf("contracts count = %d, want 2", len(msg.Contracts))
	}
	if msg.Contracts[0] != "auth-api-v1" {
		t.Errorf("contracts[0] = %q, want 'auth-api-v1'", msg.Contracts[0])
	}
}

func TestSubmitPlan_MissingPlan(t *testing.T) {
	poolDir := setupConciergePool(t)
	srv := buildConciergeTestServer(t, poolDir)

	result := callTool(t, srv, "submit_plan", map[string]any{})
	if !result.IsError {
		t.Error("expected error for missing plan")
	}
}

// --- pool_check_status ---

func setupTaskboard(t *testing.T, poolDir string, tasks map[string]*taskboard.Task) {
	t.Helper()
	board := &taskboard.Board{Version: 1, Tasks: tasks}
	if err := board.Save(filepath.Join(poolDir, "taskboard.json")); err != nil {
		t.Fatalf("saving taskboard: %v", err)
	}
}

func TestCheckStatus_SingleTask(t *testing.T) {
	poolDir := setupConciergePool(t)
	srv := buildConciergeTestServer(t, poolDir)

	setupTaskboard(t, poolDir, map[string]*taskboard.Task{
		"task-001": {
			ID:      "task-001",
			Status:  taskboard.StatusActive,
			Expert:  "auth",
			From:    "architect",
			Type:    "task",
			Priority: "normal",
		},
	})

	result := callTool(t, srv, "check_status", map[string]any{
		"task_id": "task-001",
	})

	text := resultText(t, result)
	if !strings.Contains(text, "task-001") {
		t.Errorf("result %q does not contain task ID", text)
	}
	if !strings.Contains(text, "active") {
		t.Errorf("result %q does not contain status", text)
	}
}

func TestCheckStatus_FilterByExpert(t *testing.T) {
	poolDir := setupConciergePool(t)
	srv := buildConciergeTestServer(t, poolDir)

	now := time.Now()
	setupTaskboard(t, poolDir, map[string]*taskboard.Task{
		"task-001": {ID: "task-001", Status: taskboard.StatusActive, Expert: "auth", CreatedAt: now},
		"task-002": {ID: "task-002", Status: taskboard.StatusActive, Expert: "frontend", CreatedAt: now},
		"task-003": {ID: "task-003", Status: taskboard.StatusPending, Expert: "auth", CreatedAt: now},
	})

	result := callTool(t, srv, "check_status", map[string]any{
		"expert": "auth",
	})

	text := resultText(t, result)
	if !strings.Contains(text, "task-001") {
		t.Errorf("result should contain auth task-001")
	}
	if !strings.Contains(text, "task-003") {
		t.Errorf("result should contain auth task-003")
	}
	if strings.Contains(text, "task-002") {
		t.Errorf("result should not contain frontend task-002")
	}
}

func TestCheckStatus_FilterByStatus(t *testing.T) {
	poolDir := setupConciergePool(t)
	srv := buildConciergeTestServer(t, poolDir)

	now := time.Now()
	setupTaskboard(t, poolDir, map[string]*taskboard.Task{
		"task-001": {ID: "task-001", Status: taskboard.StatusActive, Expert: "auth", CreatedAt: now},
		"task-002": {ID: "task-002", Status: taskboard.StatusCompleted, Expert: "auth", CreatedAt: now},
	})

	result := callTool(t, srv, "check_status", map[string]any{
		"status": "completed",
	})

	text := resultText(t, result)
	if !strings.Contains(text, "task-002") {
		t.Errorf("result should contain completed task-002")
	}
	if strings.Contains(text, "task-001") {
		t.Errorf("result should not contain active task-001")
	}
}

func TestCheckStatus_DefaultExcludesTerminal(t *testing.T) {
	poolDir := setupConciergePool(t)
	srv := buildConciergeTestServer(t, poolDir)

	now := time.Now()
	setupTaskboard(t, poolDir, map[string]*taskboard.Task{
		"task-001": {ID: "task-001", Status: taskboard.StatusActive, Expert: "auth", CreatedAt: now},
		"task-002": {ID: "task-002", Status: taskboard.StatusCompleted, Expert: "auth", CreatedAt: now},
		"task-003": {ID: "task-003", Status: taskboard.StatusFailed, Expert: "frontend", CreatedAt: now},
	})

	result := callTool(t, srv, "check_status", map[string]any{})

	text := resultText(t, result)
	if !strings.Contains(text, "task-001") {
		t.Errorf("result should contain active task-001")
	}
	if strings.Contains(text, "task-002") {
		t.Errorf("result should not contain completed task-002")
	}
	if strings.Contains(text, "task-003") {
		t.Errorf("result should not contain failed task-003")
	}
}

func TestCheckStatus_NotFound(t *testing.T) {
	poolDir := setupConciergePool(t)
	srv := buildConciergeTestServer(t, poolDir)

	setupTaskboard(t, poolDir, map[string]*taskboard.Task{})

	result := callTool(t, srv, "check_status", map[string]any{
		"task_id": "nonexistent",
	})
	if !result.IsError {
		t.Error("expected error for nonexistent task")
	}
}

// --- pool_list_experts ---

func TestListExperts_Happy(t *testing.T) {
	poolDir := setupConciergePool(t)
	srv := buildConciergeTestServer(t, poolDir)

	// Write pool.toml with experts
	poolToml := `[pool]
name = "test-pool"
project_dir = "/tmp/test"

[shared]
include = ["docs"]

[experts.auth]
model = "sonnet"

[experts.frontend]
model = "sonnet"
`
	if err := os.WriteFile(filepath.Join(poolDir, "pool.toml"), []byte(poolToml), 0o644); err != nil {
		t.Fatalf("writing pool.toml: %v", err)
	}

	result := callTool(t, srv, "list_experts", map[string]any{})

	text := resultText(t, result)

	var parsed map[string][]string
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("parsing result JSON: %v", err)
	}

	poolExperts := parsed["experts"]
	if len(poolExperts) != 2 {
		t.Fatalf("experts count = %d, want 2", len(poolExperts))
	}
	// Sorted alphabetically
	if poolExperts[0] != "auth" || poolExperts[1] != "frontend" {
		t.Errorf("experts = %v, want [auth, frontend]", poolExperts)
	}

	shared := parsed["shared_experts"]
	if len(shared) != 1 || shared[0] != "docs" {
		t.Errorf("shared_experts = %v, want [docs]", shared)
	}
}

func TestListExperts_MissingConfig(t *testing.T) {
	poolDir := setupConciergePool(t)
	srv := buildConciergeTestServer(t, poolDir)

	// No pool.toml written — LoadPool should fail
	result := callTool(t, srv, "list_experts", map[string]any{})
	if !result.IsError {
		t.Error("expected error for missing pool.toml")
	}
	text := resultText(t, result)
	if !strings.Contains(text, "loading pool config") {
		t.Errorf("error %q should mention config loading failure", text)
	}
}
