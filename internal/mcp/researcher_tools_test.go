// Test plan for researcher_tools.go:
//
// RegisterResearcherTools (INTEGRATION)
//   [x] All 6 researcher tools + 6 expert tools registered
//
// list_experts (FILESYSTEM I/O)
//   [x] Happy: returns experts with state sizes and log counts
//   [x] Edge: empty pool returns empty list
//
// read_expert_state (FILESYSTEM I/O)
//   [x] Happy: reads all state files
//   [x] Happy: reads single file (identity only)
//   [x] Error: missing expert param
//
// read_expert_logs (FILESYSTEM I/O)
//   [x] Happy: returns last N log entries
//   [x] Happy: query filters entries
//   [x] Edge: empty logs dir returns no entries
//
// enrich_state (FILESYSTEM I/O)
//   [x] Happy: returns assembled context with logs
//
// write_expert_state (FILESYSTEM I/O)
//   [x] Happy: writes curated state.md
//   [x] Happy: writes curated errors.md
//   [x] Error: empty content
//
// promote_pattern (FILESYSTEM I/O)
//   [x] Happy: pattern appended to existing identity.md
//   [x] Happy: creates section if absent
//   [x] Edge: empty identity.md gets header + pattern

package mcp_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cameronsjo/agent-pool/internal/expert"
)

// setupResearcherPool creates a pool directory with a researcher and target experts.
func setupResearcherPool(t *testing.T) (poolDir string) {
	t.Helper()
	poolDir = makePoolDirs(t,
		"researcher/inbox",
		"researcher/logs",
		"postoffice",
		"experts/auth/inbox",
		"experts/auth/logs",
		"experts/billing/inbox",
		"experts/billing/logs",
	)

	// Write pool.toml
	poolToml := `[pool]
name = "test-pool"
project_dir = "` + poolDir + `"

[experts.auth]
model = "sonnet"

[experts.billing]
model = "haiku"
`
	os.WriteFile(filepath.Join(poolDir, "pool.toml"), []byte(poolToml), 0o644)

	// Write state files for auth expert
	authDir := filepath.Join(poolDir, "experts", "auth")
	os.WriteFile(filepath.Join(authDir, "identity.md"), []byte("# Auth Expert\n\nHandles authentication.\n"), 0o644)
	os.WriteFile(filepath.Join(authDir, "state.md"), []byte("OAuth tokens cached.\n"), 0o644)
	os.WriteFile(filepath.Join(authDir, "errors.md"), []byte("### 2026-04-01T00:00:00Z\n\nToken refresh failed.\n"), 0o644)

	// Write some log files for auth
	expert.WriteLog(authDir, "task-001", []byte(`{"type":"result","result":"Built auth endpoint"}`))
	expert.WriteLog(authDir, "task-002", []byte(`{"type":"result","result":"Fixed OAuth bug"}`))
	expert.AppendIndex(authDir, &expert.LogEntry{TaskID: "task-001", From: "architect", ExitCode: 0, Summary: "Built auth endpoint"})
	expert.AppendIndex(authDir, &expert.LogEntry{TaskID: "task-002", From: "architect", ExitCode: 0, Summary: "Fixed OAuth bug"})

	return poolDir
}

func TestResearcherTools_Registered(t *testing.T) {
	poolDir := setupResearcherPool(t)
	srv := buildMCPTestServer(t, poolDir, "researcher", "researcher")

	names := listToolNames(t, srv)

	// Expert tools (6)
	for _, tool := range []string{"read_state", "update_state", "append_error", "send_response", "recall", "search_index"} {
		if !names[tool] {
			t.Errorf("missing expert tool %q", tool)
		}
	}

	// Researcher tools (6)
	for _, tool := range []string{"list_experts", "read_expert_state", "read_expert_logs", "enrich_state", "write_expert_state", "promote_pattern"} {
		if !names[tool] {
			t.Errorf("missing researcher tool %q", tool)
		}
	}

	if len(names) != 12 {
		t.Errorf("expected 12 tools, got %d: %v", len(names), names)
	}
}

func TestResearcherListExperts(t *testing.T) {
	poolDir := setupResearcherPool(t)
	srv := buildMCPTestServer(t, poolDir, "researcher", "researcher")

	result := callTool(t, srv, "list_experts", nil)
	text := resultText(t, result)

	var experts []struct {
		Name       string `json:"name"`
		Type       string `json:"type"`
		StateBytes int64  `json:"state_bytes"`
		LogCount   int    `json:"log_count"`
		LastTask   string `json:"last_task"`
	}
	if err := json.Unmarshal([]byte(text), &experts); err != nil {
		t.Fatalf("unmarshaling: %v\nraw: %s", err, text)
	}

	if len(experts) != 2 {
		t.Fatalf("expected 2 experts, got %d", len(experts))
	}

	// Find auth
	var auth *struct {
		Name       string `json:"name"`
		Type       string `json:"type"`
		StateBytes int64  `json:"state_bytes"`
		LogCount   int    `json:"log_count"`
		LastTask   string `json:"last_task"`
	}
	for i := range experts {
		if experts[i].Name == "auth" {
			auth = &experts[i]
			break
		}
	}
	if auth == nil {
		t.Fatal("auth expert not found")
	}
	if auth.Type != "pool" {
		t.Errorf("auth type = %q, want pool", auth.Type)
	}
	if auth.StateBytes == 0 {
		t.Error("auth state_bytes should be > 0")
	}
	if auth.LogCount != 2 {
		t.Errorf("auth log_count = %d, want 2", auth.LogCount)
	}
	if auth.LastTask != "task-002" {
		t.Errorf("auth last_task = %q, want task-002", auth.LastTask)
	}
}

func TestResearcherListExperts_Empty(t *testing.T) {
	poolDir := makePoolDirs(t, "researcher/inbox", "postoffice")
	poolToml := `[pool]
name = "empty-pool"
project_dir = "` + poolDir + `"
`
	os.WriteFile(filepath.Join(poolDir, "pool.toml"), []byte(poolToml), 0o644)

	srv := buildMCPTestServer(t, poolDir, "researcher", "researcher")
	result := callTool(t, srv, "list_experts", nil)
	text := resultText(t, result)

	var experts []any
	json.Unmarshal([]byte(text), &experts)
	if len(experts) != 0 {
		t.Errorf("expected empty list, got %d", len(experts))
	}
}

func TestResearcherReadExpertState_All(t *testing.T) {
	poolDir := setupResearcherPool(t)
	srv := buildMCPTestServer(t, poolDir, "researcher", "researcher")

	result := callTool(t, srv, "read_expert_state", map[string]any{
		"expert": "auth",
	})
	text := resultText(t, result)

	var state map[string]string
	if err := json.Unmarshal([]byte(text), &state); err != nil {
		t.Fatalf("unmarshaling: %v", err)
	}

	if !strings.Contains(state["identity"], "Auth Expert") {
		t.Errorf("identity should contain 'Auth Expert', got: %s", state["identity"])
	}
	if !strings.Contains(state["state"], "OAuth tokens") {
		t.Errorf("state should contain 'OAuth tokens', got: %s", state["state"])
	}
	if !strings.Contains(state["errors"], "Token refresh") {
		t.Errorf("errors should contain 'Token refresh', got: %s", state["errors"])
	}
}

func TestResearcherReadExpertState_SingleFile(t *testing.T) {
	poolDir := setupResearcherPool(t)
	srv := buildMCPTestServer(t, poolDir, "researcher", "researcher")

	result := callTool(t, srv, "read_expert_state", map[string]any{
		"expert": "auth",
		"file":   "identity",
	})
	text := resultText(t, result)

	if !strings.Contains(text, "Auth Expert") {
		t.Errorf("expected identity content, got: %s", text)
	}
}

func TestResearcherReadExpertState_MissingExpert(t *testing.T) {
	poolDir := setupResearcherPool(t)
	srv := buildMCPTestServer(t, poolDir, "researcher", "researcher")

	result := callTool(t, srv, "read_expert_state", map[string]any{})
	if !result.IsError {
		t.Error("expected error for missing expert param")
	}
}

func TestResearcherReadExpertLogs(t *testing.T) {
	poolDir := setupResearcherPool(t)
	srv := buildMCPTestServer(t, poolDir, "researcher", "researcher")

	result := callTool(t, srv, "read_expert_logs", map[string]any{
		"expert": "auth",
	})
	text := resultText(t, result)

	if !strings.Contains(text, "task-001") {
		t.Errorf("should contain task-001, got: %s", text)
	}
	if !strings.Contains(text, "task-002") {
		t.Errorf("should contain task-002, got: %s", text)
	}
}

func TestResearcherReadExpertLogs_Query(t *testing.T) {
	poolDir := setupResearcherPool(t)
	srv := buildMCPTestServer(t, poolDir, "researcher", "researcher")

	result := callTool(t, srv, "read_expert_logs", map[string]any{
		"expert": "auth",
		"query":  "OAuth",
	})
	text := resultText(t, result)

	if !strings.Contains(text, "task-002") {
		t.Errorf("should contain task-002 (OAuth bug), got: %s", text)
	}
	if strings.Contains(text, "task-001") {
		t.Errorf("should NOT contain task-001 (not matching OAuth), got: %s", text)
	}
}

func TestResearcherReadExpertLogs_Empty(t *testing.T) {
	poolDir := setupResearcherPool(t)
	srv := buildMCPTestServer(t, poolDir, "researcher", "researcher")

	result := callTool(t, srv, "read_expert_logs", map[string]any{
		"expert": "billing",
	})
	text := resultText(t, result)

	if !strings.Contains(text, "no log entries") {
		t.Errorf("expected 'no log entries', got: %s", text)
	}
}

func TestResearcherEnrichState(t *testing.T) {
	poolDir := setupResearcherPool(t)
	srv := buildMCPTestServer(t, poolDir, "researcher", "researcher")

	result := callTool(t, srv, "enrich_state", map[string]any{
		"expert": "auth",
	})
	text := resultText(t, result)

	var enriched map[string]any
	if err := json.Unmarshal([]byte(text), &enriched); err != nil {
		t.Fatalf("unmarshaling: %v", err)
	}

	if id, ok := enriched["identity"].(string); !ok || !strings.Contains(id, "Auth Expert") {
		t.Errorf("identity missing or wrong: %v", enriched["identity"])
	}
	if s, ok := enriched["state"].(string); !ok || !strings.Contains(s, "OAuth") {
		t.Errorf("state missing or wrong: %v", enriched["state"])
	}

	recentIndex, ok := enriched["recent_index"].([]any)
	if !ok {
		t.Fatalf("recent_index not an array: %T", enriched["recent_index"])
	}
	if len(recentIndex) != 2 {
		t.Errorf("expected 2 index entries, got %d", len(recentIndex))
	}

	recentLogs, ok := enriched["recent_logs"].([]any)
	if !ok {
		t.Fatalf("recent_logs not an array: %T", enriched["recent_logs"])
	}
	if len(recentLogs) != 2 {
		t.Errorf("expected 2 log files, got %d", len(recentLogs))
	}
}

func TestResearcherWriteExpertState(t *testing.T) {
	poolDir := setupResearcherPool(t)
	srv := buildMCPTestServer(t, poolDir, "researcher", "researcher")

	result := callTool(t, srv, "write_expert_state", map[string]any{
		"expert":  "auth",
		"content": "Curated: OAuth tokens managed via refresh flow.",
	})
	text := resultText(t, result)

	if !strings.Contains(text, "state.md updated") {
		t.Errorf("expected update confirmation, got: %s", text)
	}

	// Verify file was written
	data, err := os.ReadFile(filepath.Join(poolDir, "experts", "auth", "state.md"))
	if err != nil {
		t.Fatalf("reading state.md: %v", err)
	}
	if !strings.Contains(string(data), "Curated") {
		t.Errorf("state.md content wrong: %s", string(data))
	}
}

func TestResearcherWriteExpertState_Errors(t *testing.T) {
	poolDir := setupResearcherPool(t)
	srv := buildMCPTestServer(t, poolDir, "researcher", "researcher")

	result := callTool(t, srv, "write_expert_state", map[string]any{
		"expert":  "auth",
		"content": "Curated error log.",
		"file":    "errors",
	})
	text := resultText(t, result)

	if !strings.Contains(text, "errors.md updated") {
		t.Errorf("expected update confirmation, got: %s", text)
	}

	data, err := os.ReadFile(filepath.Join(poolDir, "experts", "auth", "errors.md"))
	if err != nil {
		t.Fatalf("reading errors.md: %v", err)
	}
	if !strings.Contains(string(data), "Curated error log") {
		t.Errorf("errors.md content wrong: %s", string(data))
	}
}

func TestResearcherWriteExpertState_EmptyContent(t *testing.T) {
	poolDir := setupResearcherPool(t)
	srv := buildMCPTestServer(t, poolDir, "researcher", "researcher")

	result := callTool(t, srv, "write_expert_state", map[string]any{
		"expert":  "auth",
		"content": "",
	})
	if !result.IsError {
		t.Error("expected error for empty content")
	}
}

func TestResearcherPromotePattern_ExistingIdentity(t *testing.T) {
	poolDir := setupResearcherPool(t)
	srv := buildMCPTestServer(t, poolDir, "researcher", "researcher")

	result := callTool(t, srv, "promote_pattern", map[string]any{
		"expert":  "auth",
		"pattern": "- Always validate token expiry before API calls",
	})
	text := resultText(t, result)

	if !strings.Contains(text, "promoted") {
		t.Errorf("expected promotion confirmation, got: %s", text)
	}

	data, err := os.ReadFile(filepath.Join(poolDir, "experts", "auth", "identity.md"))
	if err != nil {
		t.Fatalf("reading identity.md: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "## Graduated Patterns") {
		t.Error("missing Graduated Patterns section")
	}
	if !strings.Contains(content, "Always validate token expiry") {
		t.Error("missing promoted pattern")
	}
	if !strings.Contains(content, "Auth Expert") {
		t.Error("original content should be preserved")
	}
}

func TestResearcherPromotePattern_CreatesSection(t *testing.T) {
	poolDir := setupResearcherPool(t)

	// Write identity without the section
	authDir := filepath.Join(poolDir, "experts", "auth")
	os.WriteFile(filepath.Join(authDir, "identity.md"), []byte("# Auth Expert\n\nHandles auth.\n"), 0o644)

	srv := buildMCPTestServer(t, poolDir, "researcher", "researcher")

	callTool(t, srv, "promote_pattern", map[string]any{
		"expert":  "auth",
		"pattern": "- Use refresh tokens, not long-lived access tokens",
	})

	data, _ := os.ReadFile(filepath.Join(authDir, "identity.md"))
	content := string(data)

	if !strings.Contains(content, "## Graduated Patterns") {
		t.Error("section should be created")
	}
	if !strings.Contains(content, "refresh tokens") {
		t.Error("pattern should be present")
	}
}

func TestResearcherPromotePattern_EmptyIdentity(t *testing.T) {
	poolDir := setupResearcherPool(t)

	// Remove identity file to test cold-start
	authDir := filepath.Join(poolDir, "experts", "auth")
	os.Remove(filepath.Join(authDir, "identity.md"))

	srv := buildMCPTestServer(t, poolDir, "researcher", "researcher")

	callTool(t, srv, "promote_pattern", map[string]any{
		"expert":  "auth",
		"pattern": "- First promoted pattern",
	})

	data, err := os.ReadFile(filepath.Join(authDir, "identity.md"))
	if err != nil {
		t.Fatalf("identity.md should exist after promotion: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "## Graduated Patterns") {
		t.Error("section should be created")
	}
	if !strings.Contains(content, "First promoted pattern") {
		t.Error("pattern should be present")
	}
}
