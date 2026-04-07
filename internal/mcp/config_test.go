// Test plan for config.go:
//
// WriteTempConfig:
//   - Creates valid JSON file
//   - JSON matches expected MCP config schema
//   - File path is returned and file exists
//   - Caller can remove the file
//
// WriteTempConfigShared:
//   - Creates valid JSON with --shared true in args

package mcp_test

import (
	"encoding/json"
	"os"
	"testing"

	agentmcp "github.com/cameronsjo/agent-pool/internal/mcp"
)

func TestWriteTempConfig_ValidJSON(t *testing.T) {
	path, err := agentmcp.WriteTempConfig("/tmp/test-pool", "auth")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading temp file: %v", err)
	}

	var cfg agentmcp.MCPConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v\ncontent: %s", err, string(data))
	}

	entry, ok := cfg.MCPServers["agent-pool"]
	if !ok {
		t.Fatal("missing 'agent-pool' entry in mcpServers")
	}

	if entry.Type != "stdio" {
		t.Errorf("Type = %q, want %q", entry.Type, "stdio")
	}

	if entry.Command == "" {
		t.Error("Command is empty")
	}

	// Verify args contain the expected structure
	expectedArgs := []string{"mcp", "--pool", "/tmp/test-pool", "--expert", "auth"}
	if len(entry.Args) != len(expectedArgs) {
		t.Fatalf("Args = %v, want %v", entry.Args, expectedArgs)
	}
	for i, want := range expectedArgs {
		if entry.Args[i] != want {
			t.Errorf("Args[%d] = %q, want %q", i, entry.Args[i], want)
		}
	}
}

func TestWriteTempConfig_FileCleanup(t *testing.T) {
	path, err := agentmcp.WriteTempConfig("/tmp/test-pool", "auth")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// File should exist
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("temp file does not exist after creation")
	}

	// Remove should succeed
	if err := os.Remove(path); err != nil {
		t.Fatalf("removing temp file: %v", err)
	}

	// File should be gone
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("temp file still exists after removal")
	}
}

func TestWriteTempConfigShared_IncludesSharedFlag(t *testing.T) {
	path, err := agentmcp.WriteTempConfigShared("/tmp/test-pool", "security-standards")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.Remove(path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading temp file: %v", err)
	}

	var cfg agentmcp.MCPConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	entry := cfg.MCPServers["agent-pool"]
	expectedArgs := []string{"mcp", "--pool", "/tmp/test-pool", "--expert", "security-standards", "--shared", "true"}
	if len(entry.Args) != len(expectedArgs) {
		t.Fatalf("Args = %v, want %v", entry.Args, expectedArgs)
	}
	for i, want := range expectedArgs {
		if entry.Args[i] != want {
			t.Errorf("Args[%d] = %q, want %q", i, entry.Args[i], want)
		}
	}
}
