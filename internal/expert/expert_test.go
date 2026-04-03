package expert_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.sjo.lol/cameron/agent-pool/internal/expert"
	"git.sjo.lol/cameron/agent-pool/internal/mail"
)

func TestAssemblePrompt_AllFiles(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "identity.md"), []byte("I am the auth expert."), 0o644)
	os.WriteFile(filepath.Join(dir, "state.md"), []byte("Currently working on OAuth."), 0o644)
	os.WriteFile(filepath.Join(dir, "errors.md"), []byte("- JWT lib panics on empty string"), 0o644)

	cfg := &expert.SpawnConfig{
		Name:      "auth",
		ExpertDir: dir,
		TaskMessage: &mail.Message{
			ID:        "task-042",
			From:      "architect",
			Type:      mail.TypeTask,
			Priority:  mail.PriorityHigh,
			Contracts: []string{"contract-007", "contract-008"},
			Body:      "Build the token endpoint.",
		},
	}

	prompt, err := expert.AssemblePrompt(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify all sections are present
	for _, want := range []string{
		"## Expert Identity",
		"I am the auth expert.",
		"## Current State",
		"Currently working on OAuth.",
		"## Known Errors & Pitfalls",
		"JWT lib panics on empty string",
		"## Task",
		"Build the token endpoint.",
		"### Task Metadata",
		"- ID: task-042",
		"- From: architect",
		"- Type: task",
		"- Priority: high",
		"- Contracts: contract-007, contract-008",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
}

func TestAssemblePrompt_MissingOptionalFiles(t *testing.T) {
	dir := t.TempDir()
	// Only identity.md exists — state.md and errors.md are missing

	os.WriteFile(filepath.Join(dir, "identity.md"), []byte("I am the frontend expert."), 0o644)

	cfg := &expert.SpawnConfig{
		Name:      "frontend",
		ExpertDir: dir,
		TaskMessage: &mail.Message{
			ID:       "task-001",
			From:     "concierge",
			Type:     mail.TypeQuestion,
			Priority: mail.PriorityNormal,
			Body:     "What component patterns fit?",
		},
	}

	prompt, err := expert.AssemblePrompt(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Identity and task should be present
	if !strings.Contains(prompt, "## Expert Identity") {
		t.Error("prompt missing identity section")
	}
	if !strings.Contains(prompt, "## Task") {
		t.Error("prompt missing task section")
	}

	// State and errors sections should be omitted (not present as empty)
	if strings.Contains(prompt, "## Current State") {
		t.Error("prompt should not include empty state section")
	}
	if strings.Contains(prompt, "## Known Errors") {
		t.Error("prompt should not include empty errors section")
	}

	// Contracts should show "none"
	if !strings.Contains(prompt, "- Contracts: none") {
		t.Error("prompt should show 'none' for empty contracts")
	}
}

func TestAssemblePrompt_NoTaskMessage(t *testing.T) {
	dir := t.TempDir()

	cfg := &expert.SpawnConfig{
		Name:        "auth",
		ExpertDir:   dir,
		TaskMessage: nil,
	}

	_, err := expert.AssemblePrompt(cfg)
	if err == nil {
		t.Fatal("expected error for nil task message")
	}
}

func TestExtractSummary_ValidStreamJSON(t *testing.T) {
	output := `{"type":"start","session_id":"abc"}
{"type":"tool_use","name":"Read","input":{}}
{"type":"result","result":"Successfully implemented the token endpoint with PKCE support."}
`
	summary := expert.ExtractSummary([]byte(output))
	if !strings.Contains(summary, "token endpoint") {
		t.Errorf("summary = %q, expected to contain 'token endpoint'", summary)
	}
}

func TestExtractSummary_NoResultMessage(t *testing.T) {
	output := `{"type":"start","session_id":"abc"}
{"type":"tool_use","name":"Read","input":{}}
`
	summary := expert.ExtractSummary([]byte(output))
	if summary != "(no summary available)" {
		t.Errorf("summary = %q, want fallback", summary)
	}
}

func TestExtractSummary_EmptyOutput(t *testing.T) {
	summary := expert.ExtractSummary([]byte{})
	if summary != "(no summary available)" {
		t.Errorf("summary = %q, want fallback", summary)
	}
}

func TestExtractSummary_Truncation(t *testing.T) {
	long := strings.Repeat("x", 300)
	output := `{"type":"result","result":"` + long + `"}`
	summary := expert.ExtractSummary([]byte(output))
	if len(summary) > 210 { // 200 + "..."
		t.Errorf("summary length = %d, should be truncated", len(summary))
	}
	if !strings.HasSuffix(summary, "...") {
		t.Error("truncated summary should end with ...")
	}
}

func TestWriteLog(t *testing.T) {
	dir := t.TempDir()
	output := []byte(`{"type":"result","result":"done"}`)

	if err := expert.WriteLog(dir, "task-042", output); err != nil {
		t.Fatalf("WriteLog failed: %v", err)
	}

	path := filepath.Join(dir, "logs", "task-042.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading log file: %v", err)
	}
	if string(data) != string(output) {
		t.Errorf("log content mismatch")
	}
}

func TestAppendIndex(t *testing.T) {
	dir := t.TempDir()

	entry := &expert.LogEntry{
		TaskID:    "task-042",
		Timestamp: time.Date(2026, 4, 1, 14, 32, 0, 0, time.UTC),
		From:      "architect",
		ExitCode:  0,
		Summary:   "Implemented token endpoint",
	}

	// First write creates the file with header
	if err := expert.AppendIndex(dir, entry); err != nil {
		t.Fatalf("AppendIndex (first) failed: %v", err)
	}

	// Second write appends
	entry2 := &expert.LogEntry{
		TaskID:    "task-043",
		Timestamp: time.Date(2026, 4, 1, 15, 0, 0, 0, time.UTC),
		From:      "concierge",
		ExitCode:  1,
		Summary:   "Answered auth question",
	}
	if err := expert.AppendIndex(dir, entry2); err != nil {
		t.Fatalf("AppendIndex (second) failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "logs", "index.md"))
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if !strings.Contains(content, "| Task ID |") {
		t.Error("missing table header")
	}
	if !strings.Contains(content, "| Exit |") {
		t.Error("missing Exit column in table header")
	}
	if !strings.Contains(content, "task-042") {
		t.Error("missing first entry")
	}
	if !strings.Contains(content, "task-043") {
		t.Error("missing second entry")
	}
	// Verify exit codes appear in the rows
	if !strings.Contains(content, "| 0 |") {
		t.Error("missing exit code 0 in first entry")
	}
	if !strings.Contains(content, "| 1 |") {
		t.Error("missing exit code 1 in second entry")
	}
}

func TestExtractSummary_MultipleResults_UsesLast(t *testing.T) {
	output := `{"type":"result","result":"first result"}
{"type":"result","result":"second and final result"}
`
	summary := expert.ExtractSummary([]byte(output))
	if summary != "second and final result" {
		t.Errorf("summary = %q, want %q", summary, "second and final result")
	}
}

func TestExtractSummary_MalformedMixedWithValid(t *testing.T) {
	output := `not json at all
{"type":"tool_use","name":"Read"}
{broken json
{"type":"result","result":"survived the chaos"}
`
	summary := expert.ExtractSummary([]byte(output))
	if summary != "survived the chaos" {
		t.Errorf("summary = %q, want %q", summary, "survived the chaos")
	}
}

func TestExtractSummary_WhitespaceOnlyResult(t *testing.T) {
	output := "{\"type\":\"result\",\"result\":\"   \\n  \\t  \"}"
	summary := expert.ExtractSummary([]byte(output))
	if summary != "(no summary available)" {
		t.Errorf("summary = %q, want fallback %q", summary, "(no summary available)")
	}
}

func TestAssemblePrompt_EmptyFiles(t *testing.T) {
	dir := t.TempDir()

	// Write files with only whitespace — readOptionalFile trims, returns ""
	os.WriteFile(filepath.Join(dir, "identity.md"), []byte("  \n  "), 0o644)
	os.WriteFile(filepath.Join(dir, "state.md"), []byte("  \n  "), 0o644)
	os.WriteFile(filepath.Join(dir, "errors.md"), []byte("  \n  "), 0o644)

	cfg := &expert.SpawnConfig{
		Name:      "empty-files",
		ExpertDir: dir,
		TaskMessage: &mail.Message{
			ID:       "task-100",
			From:     "architect",
			Type:     mail.TypeTask,
			Priority: mail.PriorityNormal,
			Body:     "Do something.",
		},
	}

	prompt, err := expert.AssemblePrompt(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Sections for empty files should be omitted
	if strings.Contains(prompt, "## Expert Identity") {
		t.Error("prompt should not include identity section for whitespace-only file")
	}
	if strings.Contains(prompt, "## Current State") {
		t.Error("prompt should not include state section for whitespace-only file")
	}
	if strings.Contains(prompt, "## Known Errors") {
		t.Error("prompt should not include errors section for whitespace-only file")
	}

	// Task section should still be present
	if !strings.Contains(prompt, "## Task") {
		t.Error("prompt missing task section")
	}
}

func TestAssemblePrompt_EmptyTaskBody(t *testing.T) {
	dir := t.TempDir()

	cfg := &expert.SpawnConfig{
		Name:      "empty-body",
		ExpertDir: dir,
		TaskMessage: &mail.Message{
			ID:        "task-200",
			From:      "concierge",
			Type:      mail.TypeTask,
			Priority:  mail.PriorityHigh,
			Contracts: []string{"contract-001"},
			Body:      "",
		},
	}

	prompt, err := expert.AssemblePrompt(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(prompt, "## Task") {
		t.Error("prompt missing task section")
	}
	if !strings.Contains(prompt, "### Task Metadata") {
		t.Error("prompt missing task metadata")
	}
	if !strings.Contains(prompt, "- ID: task-200") {
		t.Error("prompt missing task ID")
	}
	if !strings.Contains(prompt, "- Priority: high") {
		t.Error("prompt missing priority")
	}
}

func TestAssemblePrompt_NilConfig(t *testing.T) {
	_, err := expert.AssemblePrompt(nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestAppendIndex_SanitizesAllCells(t *testing.T) {
	dir := t.TempDir()
	entry := &expert.LogEntry{
		TaskID:    "task|evil",
		Timestamp: time.Date(2026, 4, 1, 14, 32, 0, 0, time.UTC),
		From:      "arch\nitect",
		ExitCode:  0,
		Summary:   "normal summary",
	}
	if err := expert.AppendIndex(dir, entry); err != nil {
		t.Fatalf("AppendIndex failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "logs", "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Contains(content, "task|evil") {
		t.Error("TaskID pipe should be escaped")
	}
	if strings.Contains(content, "arch\nitect") {
		t.Error("From newline should be collapsed")
	}
}

func TestAppendIndex_SanitizesPipeInSummary(t *testing.T) {
	dir := t.TempDir()

	entry := &expert.LogEntry{
		TaskID:    "task-300",
		Timestamp: time.Date(2026, 4, 1, 14, 32, 0, 0, time.UTC),
		From:      "architect",
		Summary:   "choice A | choice B | choice C",
	}

	if err := expert.AppendIndex(dir, entry); err != nil {
		t.Fatalf("AppendIndex failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "logs", "index.md"))
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if !strings.Contains(content, `choice A \| choice B \| choice C`) {
		t.Errorf("pipes not escaped in index.md:\n%s", content)
	}
}

func TestAppendIndex_SanitizesNewlineInSummary(t *testing.T) {
	dir := t.TempDir()

	entry := &expert.LogEntry{
		TaskID:    "task-301",
		Timestamp: time.Date(2026, 4, 1, 14, 32, 0, 0, time.UTC),
		From:      "concierge",
		Summary:   "line one\nline two\nline three",
	}

	if err := expert.AppendIndex(dir, entry); err != nil {
		t.Fatalf("AppendIndex failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "logs", "index.md"))
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if strings.Contains(content, "\nline two") {
		t.Error("newlines in summary should be collapsed to spaces")
	}
	if !strings.Contains(content, "line one line two line three") {
		t.Errorf("newlines not collapsed in index.md:\n%s", content)
	}
}

func TestWriteStderr(t *testing.T) {
	dir := t.TempDir()
	stderr := []byte("error: something went wrong\npanic: oh no")

	if err := expert.WriteStderr(dir, "task-500", stderr); err != nil {
		t.Fatalf("WriteStderr failed: %v", err)
	}

	path := filepath.Join(dir, "logs", "task-500.stderr")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading stderr file: %v", err)
	}
	if string(data) != string(stderr) {
		t.Errorf("stderr content mismatch: got %q, want %q", string(data), string(stderr))
	}
}

// Test Plan for expert.go + log.go
//
// AssemblePrompt (Classification: I/O BOUNDARY + DATA TRANSFORMER)
//   [x] Happy: all files present (TestAssemblePrompt_AllFiles)
//   [x] Happy: missing optional files (TestAssemblePrompt_MissingOptionalFiles)
//   [x] Unhappy: no task message (TestAssemblePrompt_NoTaskMessage)
//   [x] Unhappy: nil config (TestAssemblePrompt_NilConfig)
//   [x] Boundary: empty files (exist but whitespace-only) (TestAssemblePrompt_EmptyFiles)
//   [x] Boundary: empty task body (TestAssemblePrompt_EmptyTaskBody)
//
// ExtractSummary (Classification: INPUT PARSER)
//   [x] Happy: valid stream-json (TestExtractSummary_ValidStreamJSON)
//   [x] Unhappy: no result message (TestExtractSummary_NoResultMessage)
//   [x] Boundary: empty output (TestExtractSummary_EmptyOutput)
//   [x] Boundary: truncation (TestExtractSummary_Truncation)
//   [x] Boundary: multiple results uses last (TestExtractSummary_MultipleResults_UsesLast)
//   [x] Boundary: malformed JSON mixed with valid (TestExtractSummary_MalformedMixedWithValid)
//   [x] Boundary: whitespace-only result (TestExtractSummary_WhitespaceOnlyResult)
//   [ ] Fuzz: ExtractSummary accepts arbitrary bytes — candidate for go fuzzing
//
// WriteLog (Classification: I/O BOUNDARY)
//   [x] Happy: writes log file (TestWriteLog)
//
// WriteStderr (Classification: I/O BOUNDARY)
//   [x] Happy: writes stderr file (TestWriteStderr)
//
// AppendIndex (Classification: I/O BOUNDARY)
//   [x] Happy: creates and appends entries (TestAppendIndex)
//   [x] Boundary: sanitizes pipe characters (TestAppendIndex_SanitizesPipeInSummary)
//   [x] Boundary: sanitizes newlines (TestAppendIndex_SanitizesNewlineInSummary)
//   [x] Boundary: sanitizes all cells (TestAppendIndex_SanitizesAllCells)
