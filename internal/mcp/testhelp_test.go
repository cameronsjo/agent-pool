package mcp_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	agentmcp "github.com/cameronsjo/agent-pool/internal/mcp"
)

// makePoolDirs creates a pool directory with the given subdirectories.
func makePoolDirs(t *testing.T, dirs ...string) string {
	t.Helper()
	poolDir := t.TempDir()
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(poolDir, dir), 0o755); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
	}
	return poolDir
}

// buildMCPTestServer creates an MCP server with tools registered for the given
// role. Always registers expert tools; adds role-specific tools based on role.
// Runs the MCP initialization handshake so the server accepts tool calls.
func buildMCPTestServer(t *testing.T, poolDir, expertName, role string) *server.MCPServer {
	t.Helper()
	cfg := &agentmcp.ServerConfig{
		PoolDir:      poolDir,
		ExpertName:   expertName,
		Role:         role,
		ApprovalMode: "none",
		Logger:       slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	srv := server.NewMCPServer("agent-pool-test", "0.5.0-test")
	agentmcp.RegisterExpertTools(srv, cfg)
	switch role {
	case "architect":
		agentmcp.RegisterArchitectTools(srv, cfg)
	case "concierge":
		agentmcp.RegisterConciergeTools(srv, cfg)
	}

	initMsg := mustJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":   map[string]any{},
			"clientInfo":     map[string]any{"name": "test", "version": "0.1"},
		},
	})
	srv.HandleMessage(context.Background(), initMsg)

	return srv
}

// callTool invokes a tool by name with the given arguments via JSON-RPC.
func callTool(t *testing.T, srv *server.MCPServer, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	return callToolWithContext(t, context.Background(), srv, name, args)
}

// callToolWithContext is like callTool but accepts a custom context.
// Used to test timeout behavior by passing a short-deadline context.
func callToolWithContext(t *testing.T, ctx context.Context, srv *server.MCPServer, name string, args map[string]any) *mcp.CallToolResult {
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

	resp := srv.HandleMessage(ctx, msg)
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

// listToolNames sends a tools/list request and returns a set of tool names.
func listToolNames(t *testing.T, srv *server.MCPServer) map[string]bool {
	t.Helper()

	msg := mustJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      99,
		"method":  "tools/list",
		"params":  map[string]any{},
	})

	resp := srv.HandleMessage(context.Background(), msg)
	respBytes, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshaling list response: %v", err)
	}

	var rpcResp struct {
		Result *mcp.ListToolsResult `json:"result"`
	}
	if err := json.Unmarshal(respBytes, &rpcResp); err != nil {
		t.Fatalf("unmarshaling list response: %v", err)
	}

	names := make(map[string]bool)
	if rpcResp.Result != nil {
		for _, tool := range rpcResp.Result.Tools {
			names[tool.Name] = true
		}
	}
	return names
}

// mustJSON marshals v to JSON or fails the test.
func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshaling JSON: %v", err)
	}
	return data
}
