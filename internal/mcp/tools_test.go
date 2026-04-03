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
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	agentmcp "git.sjo.lol/cameron/agent-pool/internal/mcp"
	"git.sjo.lol/cameron/agent-pool/internal/mail"
)

// setupTestPool creates a minimal pool directory structure for testing.
func setupTestPool(t *testing.T, expertName string) (poolDir string, expertDir string) {
	t.Helper()
	poolDir = t.TempDir()
	expertDir = filepath.Join(poolDir, "experts", expertName)
	if err := os.MkdirAll(expertDir, 0o755); err != nil {
		t.Fatalf("creating expert dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(expertDir, "logs"), 0o755); err != nil {
		t.Fatalf("creating logs dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(poolDir, "postoffice"), 0o755); err != nil {
		t.Fatalf("creating postoffice dir: %v", err)
	}
	return poolDir, expertDir
}

// buildTestServer creates an MCP server with expert tools registered for testing.
// It also sends the initialization handshake so the server accepts tool calls.
func buildTestServer(t *testing.T, poolDir, expertName string) *server.MCPServer {
	t.Helper()
	cfg := &agentmcp.ServerConfig{
		PoolDir:    poolDir,
		ExpertName: expertName,
		Logger:     slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	srv := server.NewMCPServer("agent-pool-test", "0.0.0-test")
	agentmcp.RegisterExpertTools(srv, cfg)

	// Initialize the server (MCP handshake)
	initMsg := mustJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":   map[string]any{},
			"clientInfo": map[string]any{
				"name":    "test-client",
				"version": "0.0.1",
			},
		},
	})
	srv.HandleMessage(context.Background(), initMsg)

	return srv
}

// callTool invokes a tool by name with the given arguments via JSON-RPC.
func callTool(t *testing.T, srv *server.MCPServer, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()

	msg := mustJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	})

	resp := srv.HandleMessage(context.Background(), msg)

	// Parse the response to extract the result
	respBytes, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshaling response: %v", err)
	}

	var rpcResp struct {
		Result *mcp.CallToolResult `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBytes, &rpcResp); err != nil {
		t.Fatalf("unmarshaling response: %v\nraw: %s", err, string(respBytes))
	}

	if rpcResp.Error != nil {
		t.Fatalf("JSON-RPC error: %d %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	if rpcResp.Result == nil {
		t.Fatalf("nil result in response: %s", string(respBytes))
	}

	return rpcResp.Result
}

// resultText extracts the text content from a tool result.
func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("result has no content")
	}

	// Content items are interfaces; marshal and re-parse to get the text
	for _, c := range result.Content {
		data, _ := json.Marshal(c)
		var tc struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(data, &tc); err == nil && tc.Type == "text" {
			return tc.Text
		}
	}

	t.Fatal("no text content found in result")
	return ""
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling JSON: %v", err)
	}
	return data
}

// --- pool_read_state ---

func TestReadState_AllPresent(t *testing.T) {
	poolDir, expertDir := setupTestPool(t, "auth")
	os.WriteFile(filepath.Join(expertDir, "identity.md"), []byte("Auth expert"), 0o644)
	os.WriteFile(filepath.Join(expertDir, "state.md"), []byte("Working on OAuth"), 0o644)
	os.WriteFile(filepath.Join(expertDir, "errors.md"), []byte("JWT panics"), 0o644)

	srv := buildTestServer(t, poolDir, "auth")
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
	poolDir, _ := setupTestPool(t, "auth")
	srv := buildTestServer(t, poolDir, "auth")
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
	poolDir, expertDir := setupTestPool(t, "auth")
	srv := buildTestServer(t, poolDir, "auth")

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
	poolDir, _ := setupTestPool(t, "auth")
	srv := buildTestServer(t, poolDir, "auth")

	result := callTool(t, srv, "pool_update_state", map[string]any{
		"content": "",
	})

	if !result.IsError {
		t.Error("expected error result for empty content")
	}
}

// --- pool_append_error ---

func TestAppendError_HappyPath(t *testing.T) {
	poolDir, expertDir := setupTestPool(t, "auth")
	srv := buildTestServer(t, poolDir, "auth")

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
	poolDir, _ := setupTestPool(t, "auth")
	srv := buildTestServer(t, poolDir, "auth")

	result := callTool(t, srv, "pool_append_error", map[string]any{
		"entry": "",
	})

	if !result.IsError {
		t.Error("expected error result for empty entry")
	}
}

// --- pool_send_response ---

func TestSendResponse_HappyPath(t *testing.T) {
	poolDir, _ := setupTestPool(t, "auth")
	srv := buildTestServer(t, poolDir, "auth")

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
	poolDir, _ := setupTestPool(t, "auth")
	srv := buildTestServer(t, poolDir, "auth")

	result := callTool(t, srv, "pool_send_response", map[string]any{
		"body": "response body",
		"id":   "resp-002",
	})

	if !result.IsError {
		t.Error("expected error result for missing 'to'")
	}
}

func TestSendResponse_PathTraversalID(t *testing.T) {
	poolDir, _ := setupTestPool(t, "auth")
	srv := buildTestServer(t, poolDir, "auth")

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
	poolDir, expertDir := setupTestPool(t, "auth")
	os.WriteFile(
		filepath.Join(expertDir, "logs", "task-042.json"),
		[]byte(`{"type":"result","result":"Built token endpoint"}`),
		0o644,
	)

	srv := buildTestServer(t, poolDir, "auth")
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
	poolDir, _ := setupTestPool(t, "auth")
	srv := buildTestServer(t, poolDir, "auth")

	result := callTool(t, srv, "pool_recall", map[string]any{
		"task_id": "nonexistent",
	})

	if !result.IsError {
		t.Error("expected error result for missing log")
	}
}

// --- pool_search_index ---

func TestSearchIndex_HappyPath(t *testing.T) {
	poolDir, expertDir := setupTestPool(t, "auth")

	index := "| Task ID | Timestamp | From | Exit | Summary |\n" +
		"|---------|-----------|------|-----:|---------|\n" +
		"| task-001 | 2026-04-01T12:00:00Z | architect | 0 | Built auth endpoint |\n" +
		"| task-002 | 2026-04-02T12:00:00Z | architect | 0 | Fixed OAuth bug |\n"
	os.WriteFile(filepath.Join(expertDir, "logs", "index.md"), []byte(index), 0o644)

	srv := buildTestServer(t, poolDir, "auth")
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
	poolDir, expertDir := setupTestPool(t, "auth")

	index := "| Task ID | Timestamp | From | Exit | Summary |\n" +
		"|---------|-----------|------|-----:|---------|\n" +
		"| task-001 | 2026-04-01T12:00:00Z | architect | 0 | Built auth endpoint |\n"
	os.WriteFile(filepath.Join(expertDir, "logs", "index.md"), []byte(index), 0o644)

	srv := buildTestServer(t, poolDir, "auth")
	result := callTool(t, srv, "pool_search_index", map[string]any{
		"query": "nonexistent",
	})

	text := resultText(t, result)
	if !strings.Contains(text, "no matching") {
		t.Errorf("result = %q, want 'no matching tasks found'", text)
	}
}
