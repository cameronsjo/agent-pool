package expert_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cameronsjo/agent-pool/internal/expert"
	"github.com/cameronsjo/agent-pool/internal/mail"
)

// ---------------------------------------------------------------------------
// TestHelperProcess — fake claude binary via subprocess re-exec
// ---------------------------------------------------------------------------
//
// This is not a real test. It's invoked as a subprocess by tests that need
// a fake "claude" binary. When GO_TEST_HELPER_PROCESS=1, the test binary
// acts as a stand-in for the claude CLI.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_TEST_HELPER_PROCESS") != "1" {
		return
	}
	// Suppress test framework output when running as helper.
	// We are a fake binary now — act accordingly.
	switch os.Getenv("TEST_HELPER_MODE") {
	case "echo-env":
		// Emit pool env vars as JSON on stdout.
		env := map[string]string{
			"AGENT_POOL_EXPERT":  os.Getenv("AGENT_POOL_EXPERT"),
			"AGENT_POOL_TASK_ID": os.Getenv("AGENT_POOL_TASK_ID"),
			"AGENT_POOL_DIR":     os.Getenv("AGENT_POOL_DIR"),
		}
		json.NewEncoder(os.Stdout).Encode(env)

	case "read-stdin":
		// Read all of stdin and echo it back on stdout.
		data, _ := io.ReadAll(os.Stdin)
		os.Stdout.Write(data)

	case "exit-code":
		code, _ := strconv.Atoi(os.Getenv("TEST_HELPER_EXIT_CODE"))
		os.Exit(code)

	case "stderr":
		// Write to both stdout and stderr.
		fmt.Fprint(os.Stdout, "stdout-content")
		fmt.Fprint(os.Stderr, "stderr-content")

	case "slow":
		// Block until killed or context cancelled. 60s is well above any
		// test timeout, so the test must cancel the context.
		time.Sleep(60 * time.Second)

	default:
		// Default: succeed silently.
	}
	os.Exit(0)
}

// fakeClaudeBin creates a shell script named "claude" in a temp directory
// that re-execs the test binary as a helper process with the given mode.
// Returns the temp dir (caller should add it to PATH).
func fakeClaudeBin(t *testing.T, mode string) string {
	t.Helper()
	binDir := t.TempDir()

	// Shell script that re-invokes the test binary with helper env vars.
	// Claude's CLI args (-p, --output-format, etc.) are intentionally dropped
	// — the test binary only needs -test.run to find TestHelperProcess.
	script := fmt.Sprintf("#!/bin/sh\nexec %q -test.run=^TestHelperProcess$\n", os.Args[0])
	scriptPath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake claude script: %v", err)
	}

	// Propagate the re-exec env vars via the environment.
	t.Setenv("GO_TEST_HELPER_PROCESS", "1")
	t.Setenv("TEST_HELPER_MODE", mode)

	return binDir
}

// spawnCfg returns a minimal SpawnConfig suitable for Spawn tests.
func spawnCfg(t *testing.T) *expert.SpawnConfig {
	t.Helper()
	expertDir := t.TempDir()
	os.WriteFile(filepath.Join(expertDir, "identity.md"), []byte("test expert"), 0o644)

	return &expert.SpawnConfig{
		Name:       "test-expert",
		Model:      "sonnet",
		ExpertDir:  expertDir,
		ProjectDir: t.TempDir(),
		PoolDir:    t.TempDir(),
		TaskMessage: &mail.Message{
			ID:       "task-spawn-001",
			From:     "architect",
			Type:     mail.TypeTask,
			Priority: mail.PriorityNormal,
			Body:     "Run the tests.",
		},
	}
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// prependPath returns the original PATH with dir prepended.
func prependPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// ---------------------------------------------------------------------------
// Spawn tests
// ---------------------------------------------------------------------------

func TestSpawn_HappyPath(t *testing.T) {
	binDir := fakeClaudeBin(t, "echo-env")
	prependPath(t, binDir)

	cfg := spawnCfg(t)
	result, err := expert.Spawn(context.Background(), testLogger(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TaskID != "task-spawn-001" {
		t.Errorf("TaskID = %q, want %q", result.TaskID, "task-spawn-001")
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	if result.PID == 0 {
		t.Error("PID should be non-zero")
	}
	if result.Duration <= 0 {
		t.Error("Duration should be positive")
	}
	if len(result.Output) == 0 {
		t.Error("Output should not be empty (helper echoes env vars)")
	}
}

func TestSpawn_EnvVarsSet(t *testing.T) {
	binDir := fakeClaudeBin(t, "echo-env")
	prependPath(t, binDir)

	cfg := spawnCfg(t)
	result, err := expert.Spawn(context.Background(), testLogger(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var env map[string]string
	if err := json.Unmarshal(result.Output, &env); err != nil {
		t.Fatalf("decoding env JSON from output: %v\nraw output: %s", err, result.Output)
	}

	checks := map[string]string{
		"AGENT_POOL_EXPERT":  cfg.Name,
		"AGENT_POOL_TASK_ID": cfg.TaskMessage.ID,
		"AGENT_POOL_DIR":     cfg.PoolDir,
	}
	for k, want := range checks {
		if got := env[k]; got != want {
			t.Errorf("env %s = %q, want %q", k, got, want)
		}
	}
}

func TestSpawn_StdinReceivesPrompt(t *testing.T) {
	binDir := fakeClaudeBin(t, "read-stdin")
	prependPath(t, binDir)

	cfg := spawnCfg(t)
	result, err := expert.Spawn(context.Background(), testLogger(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := string(result.Output)
	// The prompt should contain the task body and identity.
	if !strings.Contains(output, "Run the tests.") {
		t.Errorf("stdin prompt missing task body; got:\n%s", output)
	}
	if !strings.Contains(output, "test expert") {
		t.Errorf("stdin prompt missing identity; got:\n%s", output)
	}
}

func TestSpawn_ClaudeNotInPATH(t *testing.T) {
	// Set PATH to empty dir so LookPath fails.
	t.Setenv("PATH", t.TempDir())

	cfg := spawnCfg(t)
	_, err := expert.Spawn(context.Background(), testLogger(), cfg)
	if err == nil {
		t.Fatal("expected error when claude not in PATH")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want 'not found'", err)
	}
}

func TestSpawn_NonZeroExitCode(t *testing.T) {
	binDir := fakeClaudeBin(t, "exit-code")
	prependPath(t, binDir)
	t.Setenv("TEST_HELPER_EXIT_CODE", "42")

	cfg := spawnCfg(t)
	result, err := expert.Spawn(context.Background(), testLogger(), cfg)
	if err != nil {
		t.Fatalf("non-zero exit should not return error: %v", err)
	}
	if result.ExitCode != 42 {
		t.Errorf("ExitCode = %d, want 42", result.ExitCode)
	}
}

func TestSpawn_NilConfig(t *testing.T) {
	_, err := expert.Spawn(context.Background(), testLogger(), nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Errorf("error = %q, want mention of nil", err)
	}
}

func TestSpawn_EmptyPoolDir(t *testing.T) {
	binDir := fakeClaudeBin(t, "echo-env")
	prependPath(t, binDir)

	cfg := spawnCfg(t)
	cfg.PoolDir = ""

	_, err := expert.Spawn(context.Background(), testLogger(), cfg)
	if err == nil {
		t.Fatal("expected error for empty PoolDir")
	}
	if !strings.Contains(err.Error(), "pool directory") {
		t.Errorf("error = %q, want 'pool directory'", err)
	}
}

func TestSpawn_ContextCancellation(t *testing.T) {
	binDir := fakeClaudeBin(t, "slow")
	prependPath(t, binDir)

	cfg := spawnCfg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	result, err := expert.Spawn(ctx, testLogger(), cfg)
	if err != nil {
		t.Fatalf("context cancellation should return result, not error: %v", err)
	}
	// Process was killed — exit code should be non-zero (or -1 for timeout).
	if result.ExitCode == 0 {
		t.Error("ExitCode should be non-zero after context cancellation")
	}
}

func TestSpawn_StderrCaptured(t *testing.T) {
	binDir := fakeClaudeBin(t, "stderr")
	prependPath(t, binDir)

	cfg := spawnCfg(t)
	result, err := expert.Spawn(context.Background(), testLogger(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(string(result.Output), "stdout-content") {
		t.Errorf("stdout missing expected content; got: %s", result.Output)
	}
	if !strings.Contains(string(result.Stderr), "stderr-content") {
		t.Errorf("stderr missing expected content; got: %s", result.Stderr)
	}
}

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

func TestExtractResult_ValidStreamJSON(t *testing.T) {
	output := `{"type":"start","session_id":"abc"}
{"type":"result","result":"Successfully implemented the token endpoint with PKCE support."}
`
	result := expert.ExtractResult([]byte(output))
	if result != "Successfully implemented the token endpoint with PKCE support." {
		t.Errorf("result = %q", result)
	}
}

func TestExtractResult_NoTruncation(t *testing.T) {
	long := strings.Repeat("x", 300)
	output := `{"type":"result","result":"` + long + `"}`
	result := expert.ExtractResult([]byte(output))
	if len(result) != 300 {
		t.Errorf("result length = %d, want 300 (no truncation)", len(result))
	}
}

func TestExtractResult_EmptyOutput(t *testing.T) {
	result := expert.ExtractResult([]byte{})
	if result != "(no result available)" {
		t.Errorf("result = %q, want fallback", result)
	}
}

func TestExtractResult_MultipleResults_UsesLast(t *testing.T) {
	output := `{"type":"result","result":"first"}
{"type":"result","result":"second and final"}
`
	result := expert.ExtractResult([]byte(output))
	if result != "second and final" {
		t.Errorf("result = %q, want %q", result, "second and final")
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
// Spawn (Classification: I/O BOUNDARY)
//   [x] Happy: spawn succeeds, Result has correct TaskID, ExitCode=0, Output, Duration, PID (TestSpawn_HappyPath)
//   [x] Happy: env vars (AGENT_POOL_EXPERT, AGENT_POOL_TASK_ID, AGENT_POOL_DIR) set correctly (TestSpawn_EnvVarsSet)
//   [x] Happy: stdin receives assembled prompt (TestSpawn_StdinReceivesPrompt)
//   [x] Unhappy: claude not in PATH returns error (TestSpawn_ClaudeNotInPATH)
//   [x] Unhappy: non-zero exit code captured in Result, not returned as error (TestSpawn_NonZeroExitCode)
//   [x] Unhappy: nil SpawnConfig returns error (TestSpawn_NilConfig)
//   [x] Unhappy: empty PoolDir returns error (TestSpawn_EmptyPoolDir)
//   [x] Behavioral: context cancellation sends SIGTERM (TestSpawn_ContextCancellation)
//   [x] Behavioral: stderr captured separately from stdout (TestSpawn_StderrCaptured)
//
// AssemblePrompt (Classification: I/O BOUNDARY + DATA TRANSFORMER)
//   [x] Happy: all files present (TestAssemblePrompt_AllFiles)
//   [x] Happy: missing optional files (TestAssemblePrompt_MissingOptionalFiles)
//   [x] Unhappy: no task message (TestAssemblePrompt_NoTaskMessage)
//   [x] Unhappy: nil config (TestAssemblePrompt_NilConfig)
//   [x] Boundary: empty files (exist but whitespace-only) (TestAssemblePrompt_EmptyFiles)
//   [x] Boundary: empty task body (TestAssemblePrompt_EmptyTaskBody)
//
// ExtractResult (Classification: INPUT PARSER)
//   [x] Happy: valid stream-json (TestExtractResult_ValidStreamJSON)
//   [x] Boundary: no truncation for long output (TestExtractResult_NoTruncation)
//   [x] Boundary: empty output (TestExtractResult_EmptyOutput)
//   [x] Boundary: multiple results uses last (TestExtractResult_MultipleResults_UsesLast)
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
