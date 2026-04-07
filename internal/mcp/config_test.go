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
//
// ToolNamesForRole:
//   - Architect: expert + architect tools
//   - Researcher: expert + researcher tools
//   - Unknown/expert: expert tools only
//   - Returned slice is a copy (mutation-safe)

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

func TestToolNamesForRole_Architect(t *testing.T) {
	names := agentmcp.ToolNamesForRole("architect")

	// Should include all expert tools
	for _, tool := range agentmcp.ExpertToolNames {
		if !contains(names, tool) {
			t.Errorf("missing expert tool %q", tool)
		}
	}

	// Should include all architect tools
	for _, tool := range agentmcp.ArchitectToolNames {
		if !contains(names, tool) {
			t.Errorf("missing architect tool %q", tool)
		}
	}

	// Should NOT include researcher tools
	for _, tool := range agentmcp.ResearcherToolNames {
		if contains(names, tool) {
			t.Errorf("unexpected researcher tool %q in architect role", tool)
		}
	}
}

func TestToolNamesForRole_Researcher(t *testing.T) {
	names := agentmcp.ToolNamesForRole("researcher")

	for _, tool := range agentmcp.ExpertToolNames {
		if !contains(names, tool) {
			t.Errorf("missing expert tool %q", tool)
		}
	}

	for _, tool := range agentmcp.ResearcherToolNames {
		if !contains(names, tool) {
			t.Errorf("missing researcher tool %q", tool)
		}
	}

	for _, tool := range agentmcp.ArchitectToolNames {
		if contains(names, tool) {
			t.Errorf("unexpected architect tool %q in researcher role", tool)
		}
	}
}

func TestToolNamesForRole_Expert(t *testing.T) {
	names := agentmcp.ToolNamesForRole("auth")

	if len(names) != len(agentmcp.ExpertToolNames) {
		t.Errorf("got %d tools, want %d (expert tools only)", len(names), len(agentmcp.ExpertToolNames))
	}
}

func TestToolNamesForRole_ReturnsCopy(t *testing.T) {
	names1 := agentmcp.ToolNamesForRole("researcher")
	names2 := agentmcp.ToolNamesForRole("researcher")

	// Mutating one should not affect the other
	names1[0] = "mutated"
	if names2[0] == "mutated" {
		t.Error("ToolNamesForRole should return independent copies")
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
