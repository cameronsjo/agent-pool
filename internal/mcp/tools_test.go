// Test plan for tools.go:
//
// Each handler is tested by constructing a JSON-RPC tools/call message,
// sending it through HandleMessage, and inspecting the response.
//
// pool_read_state:
//   - All state files present → returns JSON with all three fields
//   - No state files → returns JSON with empty strings
//
// pool_update_state:
//   - Happy path → state.md written
//   - Empty content → error result
//
// pool_append_error:
//   - Happy path → errors.md contains entry
//   - Empty entry → error result
//
// pool_send_response:
//   - Happy path → message file appears in postoffice, round-trips through Parse
//   - Missing required params → error result
//
// pool_recall:
//   - Happy path → returns log content
//   - Missing log → error result
//
// pool_search_index:
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
	result := callTool(t, srv, "pool_read_state", nil)

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
	result := callTool(t, srv, "pool_read_state", nil)

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

	result := callTool(t, srv, "pool_update_state", map[string]any{
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

	result := callTool(t, srv, "pool_update_state", map[string]any{
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

	result := callTool(t, srv, "pool_append_error", map[string]any{
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

	result := callTool(t, srv, "pool_append_error", map[string]any{
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

	result := callTool(t, srv, "pool_send_response", map[string]any{
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

	result := callTool(t, srv, "pool_send_response", map[string]any{
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

	result := callTool(t, srv, "pool_send_response", map[string]any{
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
	result := callTool(t, srv, "pool_recall", map[string]any{
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

	result := callTool(t, srv, "pool_recall", map[string]any{
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
	result := callTool(t, srv, "pool_search_index", map[string]any{
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
	result := callTool(t, srv, "pool_search_index", map[string]any{
		"query": "nonexistent",
	})

	text := resultText(t, result)
	if !strings.Contains(text, "no matching") {
		t.Errorf("result = %q, want 'no matching tasks found'", text)
	}
}
