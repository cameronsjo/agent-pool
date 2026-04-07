// Test plan for tools.go (v0.7 shared expert scope):
//
//update_state (shared expert):
//   - scope=project → writes to SharedOverlayDir/state.md
//   - scope=user → writes to expertDir/state.md (user-level)
//   - default scope → project
//   - pool-scoped expert → scope ignored, writes to expertDir
//
//read_state (shared expert):
//   - returns project_state field alongside state
//
// Test plan for tools.go:
//
// Each handler is tested by constructing a JSON-RPC tools/call message,
// sending it through HandleMessage, and inspecting the response.
//
//read_state:
//   - All state files present → returns JSON with all three fields
//   - No state files → returns JSON with empty strings
//
//update_state:
//   - Happy path → state.md written
//   - Empty content → error result
//
//append_error:
//   - Happy path → errors.md contains entry
//   - Empty entry → error result
//
//send_response:
//   - Happy path → message file appears in postoffice, round-trips through Parse
//   - Missing required params → error result
//
//recall:
//   - Happy path → returns log content
//   - Missing log → error result
//
//search_index:
//   - Happy path → returns matching rows
//   - No matches → returns "no matching tasks found"

package mcp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cameronsjo/agent-pool/internal/mail"
)

// setupExpertPool creates a pool directory for an expert with postoffice + expert dir + logs.
func setupExpertPool(t *testing.T, expertName string) (poolDir string, expertDir string) {
	t.Helper()
	poolDir = makePoolDirs(t, "postoffice", filepath.Join("experts", expertName), filepath.Join("experts", expertName, "logs"))
	expertDir = filepath.Join(poolDir, "experts", expertName)
	return poolDir, expertDir
}

// --- pool_read_state ---

func TestReadState_AllPresent(t *testing.T) {
	poolDir, expertDir := setupExpertPool(t, "auth")
	os.WriteFile(filepath.Join(expertDir, "identity.md"), []byte("Auth expert"), 0o644)
	os.WriteFile(filepath.Join(expertDir, "state.md"), []byte("Working on OAuth"), 0o644)
	os.WriteFile(filepath.Join(expertDir, "errors.md"), []byte("JWT panics"), 0o644)

	srv := buildMCPTestServer(t, poolDir, "auth", "")
	result := callTool(t, srv, "read_state", nil)

	text := resultText(t, result)
	var data map[string]string
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("unmarshal error: %v\ntext: %s", err, text)
	}

	if data["identity"] != "Auth expert" {
		t.Errorf("identity = %q, want %q", data["identity"], "Auth expert")
	}
	if data["state"] != "Working on OAuth" {
		t.Errorf("state = %q, want %q", data["state"], "Working on OAuth")
	}
	if data["errors"] != "JWT panics" {
		t.Errorf("errors = %q, want %q", data["errors"], "JWT panics")
	}
}

func TestReadState_NoFiles(t *testing.T) {
	poolDir, _ := setupExpertPool(t, "auth")
	srv := buildMCPTestServer(t, poolDir, "auth", "")
	result := callTool(t, srv, "read_state", nil)

	text := resultText(t, result)
	var data map[string]string
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if data["identity"] != "" || data["state"] != "" || data["errors"] != "" {
		t.Errorf("expected all empty, got %v", data)
	}
}

// --- pool_update_state ---

func TestUpdateState_HappyPath(t *testing.T) {
	poolDir, expertDir := setupExpertPool(t, "auth")
	srv := buildMCPTestServer(t, poolDir, "auth", "")

	result := callTool(t, srv, "update_state", map[string]any{
		"content": "Updated working memory",
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, result))
	}

	data, _ := os.ReadFile(filepath.Join(expertDir, "state.md"))
	if !strings.Contains(string(data), "Updated working memory") {
		t.Errorf("state.md = %q, want to contain 'Updated working memory'", string(data))
	}
}

func TestUpdateState_EmptyContent(t *testing.T) {
	poolDir, _ := setupExpertPool(t, "auth")
	srv := buildMCPTestServer(t, poolDir, "auth", "")

	result := callTool(t, srv, "update_state", map[string]any{
		"content": "",
	})

	if !result.IsError {
		t.Error("expected error result for empty content")
	}
}

// --- pool_append_error ---

func TestAppendError_HappyPath(t *testing.T) {
	poolDir, expertDir := setupExpertPool(t, "auth")
	srv := buildMCPTestServer(t, poolDir, "auth", "")

	result := callTool(t, srv, "append_error", map[string]any{
		"entry": "Connection timeout to database",
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, result))
	}

	data, _ := os.ReadFile(filepath.Join(expertDir, "errors.md"))
	if !strings.Contains(string(data), "Connection timeout to database") {
		t.Errorf("errors.md missing entry content")
	}
}

func TestAppendError_EmptyEntry(t *testing.T) {
	poolDir, _ := setupExpertPool(t, "auth")
	srv := buildMCPTestServer(t, poolDir, "auth", "")

	result := callTool(t, srv, "append_error", map[string]any{
		"entry": "",
	})

	if !result.IsError {
		t.Error("expected error result for empty entry")
	}
}

// --- pool_send_response ---

func TestSendResponse_HappyPath(t *testing.T) {
	poolDir, _ := setupExpertPool(t, "auth")
	srv := buildMCPTestServer(t, poolDir, "auth", "")

	result := callTool(t, srv, "send_response", map[string]any{
		"to":   "architect",
		"body": "Token endpoint is complete.",
		"id":   "resp-001",
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, result))
	}

	// Verify the message file exists and round-trips
	path := filepath.Join(poolDir, "postoffice", "resp-001.md")
	msg, err := mail.ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile error: %v", err)
	}

	if msg.From != "auth" {
		t.Errorf("From = %q, want %q", msg.From, "auth")
	}
	if msg.To != "architect" {
		t.Errorf("To = %q, want %q", msg.To, "architect")
	}
	if msg.Type != mail.TypeResponse {
		t.Errorf("Type = %q, want %q", msg.Type, mail.TypeResponse)
	}
	if msg.Body != "Token endpoint is complete." {
		t.Errorf("Body = %q, want %q", msg.Body, "Token endpoint is complete.")
	}
}

func TestSendResponse_MissingTo(t *testing.T) {
	poolDir, _ := setupExpertPool(t, "auth")
	srv := buildMCPTestServer(t, poolDir, "auth", "")

	result := callTool(t, srv, "send_response", map[string]any{
		"body": "response body",
		"id":   "resp-002",
	})

	if !result.IsError {
		t.Error("expected error result for missing 'to'")
	}
}

func TestSendResponse_PathTraversalID(t *testing.T) {
	poolDir, _ := setupExpertPool(t, "auth")
	srv := buildMCPTestServer(t, poolDir, "auth", "")

	result := callTool(t, srv, "send_response", map[string]any{
		"to":   "architect",
		"body": "response body",
		"id":   "../../etc/evil",
	})

	if !result.IsError {
		t.Error("expected error result for path traversal in id")
	}
}

// --- pool_recall ---

func TestRecall_HappyPath(t *testing.T) {
	poolDir, expertDir := setupExpertPool(t, "auth")
	os.WriteFile(
		filepath.Join(expertDir, "logs", "task-042.json"),
		[]byte(`{"type":"result","result":"Built token endpoint"}`),
		0o644,
	)

	srv := buildMCPTestServer(t, poolDir, "auth", "")
	result := callTool(t, srv, "recall", map[string]any{
		"task_id": "task-042",
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	if !strings.Contains(text, "Built token endpoint") {
		t.Errorf("result = %q, want to contain 'Built token endpoint'", text)
	}
}

func TestRecall_MissingLog(t *testing.T) {
	poolDir, _ := setupExpertPool(t, "auth")
	srv := buildMCPTestServer(t, poolDir, "auth", "")

	result := callTool(t, srv, "recall", map[string]any{
		"task_id": "nonexistent",
	})

	if !result.IsError {
		t.Error("expected error result for missing log")
	}
}

// --- pool_search_index ---

func TestSearchIndex_HappyPath(t *testing.T) {
	poolDir, expertDir := setupExpertPool(t, "auth")

	index := "| Task ID | Timestamp | From | Exit | Summary |\n" +
		"|---------|-----------|------|-----:|---------|\n" +
		"| task-001 | 2026-04-01T12:00:00Z | architect | 0 | Built auth endpoint |\n" +
		"| task-002 | 2026-04-02T12:00:00Z | architect | 0 | Fixed OAuth bug |\n"
	os.WriteFile(filepath.Join(expertDir, "logs", "index.md"), []byte(index), 0o644)

	srv := buildMCPTestServer(t, poolDir, "auth", "")
	result := callTool(t, srv, "search_index", map[string]any{
		"query": "OAuth",
	})

	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	if !strings.Contains(text, "task-002") {
		t.Errorf("result = %q, want to contain 'task-002'", text)
	}
}

func TestSearchIndex_NoMatches(t *testing.T) {
	poolDir, expertDir := setupExpertPool(t, "auth")

	index := "| Task ID | Timestamp | From | Exit | Summary |\n" +
		"|---------|-----------|------|-----:|---------|\n" +
		"| task-001 | 2026-04-01T12:00:00Z | architect | 0 | Built auth endpoint |\n"
	os.WriteFile(filepath.Join(expertDir, "logs", "index.md"), []byte(index), 0o644)

	srv := buildMCPTestServer(t, poolDir, "auth", "")
	result := callTool(t, srv, "search_index", map[string]any{
		"query": "nonexistent",
	})

	text := resultText(t, result)
	if !strings.Contains(text, "no matching") {
		t.Errorf("result = %q, want 'no matching tasks found'", text)
	}
}

// --- Shared expert scoped state tests ---

// setupSharedExpertPool creates pool dirs and a user-level expert dir (simulated
// by a temp dir since we can't override HOME in unit tests).
func setupSharedExpertPool(t *testing.T) (poolDir, userExpertDir, overlayDir string) {
	t.Helper()
	poolDir = makePoolDirs(t, "postoffice", "shared-state/security-standards")
	userExpertDir = t.TempDir() // simulates ~/.agent-pool/experts/security-standards/
	overlayDir = filepath.Join(poolDir, "shared-state", "security-standards")
	return
}

func TestUpdateState_SharedExpert_ProjectScope(t *testing.T) {
	poolDir, _, overlayDir := setupSharedExpertPool(t)
	srv := buildSharedMCPTestServer(t, poolDir, "security-standards", overlayDir)

	result := callTool(t, srv, "update_state", map[string]any{
		"content": "project-specific knowledge",
		"scope":   "project",
	})

	text := resultText(t, result)
	if !strings.Contains(text, "project") {
		t.Errorf("result = %q, want mention of project", text)
	}

	// Verify file written to overlay dir
	data, err := os.ReadFile(filepath.Join(overlayDir, "state.md"))
	if err != nil {
		t.Fatalf("reading overlay state.md: %v", err)
	}
	if !strings.Contains(string(data), "project-specific knowledge") {
		t.Errorf("overlay state.md = %q, want 'project-specific knowledge'", string(data))
	}
}

func TestUpdateState_SharedExpert_UserScope(t *testing.T) {
	// Set HOME to a temp dir so ResolveSharedExpertDir resolves to a controlled path
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	// Create the user-level shared expert directory
	userExpertDir := filepath.Join(fakeHome, ".agent-pool", "experts", "security-standards")
	if err := os.MkdirAll(userExpertDir, 0o755); err != nil {
		t.Fatalf("creating shared expert dir: %v", err)
	}

	poolDir, _, overlayDir := setupSharedExpertPool(t)
	srv := buildSharedMCPTestServer(t, poolDir, "security-standards", overlayDir)

	result := callTool(t, srv, "update_state", map[string]any{
		"content": "user-level knowledge",
		"scope":   "user",
	})

	text := resultText(t, result)
	if !strings.Contains(text, "user") {
		t.Errorf("result = %q, want mention of user", text)
	}

	// Verify file written to user-level dir
	data, err := os.ReadFile(filepath.Join(userExpertDir, "state.md"))
	if err != nil {
		t.Fatalf("reading user-level state.md: %v", err)
	}
	if !strings.Contains(string(data), "user-level knowledge") {
		t.Errorf("user-level state.md = %q, want 'user-level knowledge'", string(data))
	}
}

func TestUpdateState_SharedExpert_DefaultScope(t *testing.T) {
	poolDir, _, overlayDir := setupSharedExpertPool(t)
	srv := buildSharedMCPTestServer(t, poolDir, "security-standards", overlayDir)

	// No scope param = defaults to "project"
	result := callTool(t, srv, "update_state", map[string]any{
		"content": "default-scope content",
	})

	text := resultText(t, result)
	if !strings.Contains(text, "project") {
		t.Errorf("result = %q, want mention of project (default scope)", text)
	}

	data, err := os.ReadFile(filepath.Join(overlayDir, "state.md"))
	if err != nil {
		t.Fatalf("reading overlay state.md: %v", err)
	}
	if !strings.Contains(string(data), "default-scope content") {
		t.Errorf("overlay state.md = %q, want 'default-scope content'", string(data))
	}
}

func TestUpdateState_PoolScoped_ScopeIgnored(t *testing.T) {
	poolDir, expertDir := setupExpertPool(t, "auth")

	srv := buildMCPTestServer(t, poolDir, "auth", "")
	result := callTool(t, srv, "update_state", map[string]any{
		"content": "pool-scoped state",
		"scope":   "user", // should be ignored for pool-scoped experts
	})

	text := resultText(t, result)
	if !strings.Contains(text, "updated") {
		t.Errorf("result = %q, want 'updated'", text)
	}

	// Verify written to expertDir (pool-scoped)
	data, err := os.ReadFile(filepath.Join(expertDir, "state.md"))
	if err != nil {
		t.Fatalf("reading state.md: %v", err)
	}
	if !strings.Contains(string(data), "pool-scoped state") {
		t.Errorf("state.md = %q, want 'pool-scoped state'", string(data))
	}
}

func TestReadState_SharedExpert_IncludesProjectState(t *testing.T) {
	poolDir, _, overlayDir := setupSharedExpertPool(t)

	// The expertDir for the MCP server is resolved by RegisterExpertTools.
	// For shared experts, it calls ResolveSharedExpertDir which needs HOME.
	// In unit tests, we write state files to whatever dir the server uses.
	// Since we can't control HOME here, we'll use the overlay dir test.

	// Write overlay state
	os.WriteFile(filepath.Join(overlayDir, "state.md"), []byte("Project overlay state"), 0o644)

	srv := buildSharedMCPTestServer(t, poolDir, "security-standards", overlayDir)
	result := callTool(t, srv, "read_state", map[string]any{})

	text := resultText(t, result)
	if !strings.Contains(text, "project_state") {
		t.Errorf("result should contain 'project_state' key, got: %s", text)
	}
	if !strings.Contains(text, "Project overlay state") {
		t.Errorf("result should contain overlay state content, got: %s", text)
	}
}
