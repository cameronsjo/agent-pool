// Test plan for state.go:
//
// ReadState:
//   - All three files present → returns content from each
//   - Some files missing → missing files return empty strings
//   - All files missing → all empty strings, no error
//
// WriteState:
//   - Happy path → file written, content matches
//   - Empty content → error
//   - Oversized content → error
//   - Atomic write → no partial writes visible
//
// AppendError:
//   - Happy path → entry appended with timestamp header
//   - Creates errors.md if missing
//   - Appends to existing file
//   - Empty entry → error
//
// ReadLog:
//   - Happy path → returns raw bytes
//   - Missing log → descriptive error
//   - Path traversal attempt → rejected
//   - Empty task ID → error
//
// SearchIndex:
//   - Happy path → matching rows returned
//   - No matches → empty slice
//   - Missing index file → nil, no error
//   - Case-insensitive matching

package expert_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cameronsjo/agent-pool/internal/expert"
)

func TestReadState_AllPresent(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "identity.md"), []byte("I am auth expert"), 0o644)
	os.WriteFile(filepath.Join(dir, "state.md"), []byte("Working on OAuth"), 0o644)
	os.WriteFile(filepath.Join(dir, "errors.md"), []byte("JWT panics on empty"), 0o644)

	identity, state, errors, err := expert.ReadState(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if identity != "I am auth expert" {
		t.Errorf("identity = %q, want %q", identity, "I am auth expert")
	}
	if state != "Working on OAuth" {
		t.Errorf("state = %q, want %q", state, "Working on OAuth")
	}
	if errors != "JWT panics on empty" {
		t.Errorf("errors = %q, want %q", errors, "JWT panics on empty")
	}
}

func TestReadState_SomeMissing(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "identity.md"), []byte("I am auth expert"), 0o644)
	// state.md and errors.md intentionally missing

	identity, state, errors, err := expert.ReadState(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if identity != "I am auth expert" {
		t.Errorf("identity = %q, want %q", identity, "I am auth expert")
	}
	if state != "" {
		t.Errorf("state = %q, want empty string", state)
	}
	if errors != "" {
		t.Errorf("errors = %q, want empty string", errors)
	}
}

func TestReadState_AllMissing(t *testing.T) {
	dir := t.TempDir()

	identity, state, errors, err := expert.ReadState(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if identity != "" || state != "" || errors != "" {
		t.Errorf("expected all empty, got identity=%q state=%q errors=%q", identity, state, errors)
	}
}

func TestWriteState_HappyPath(t *testing.T) {
	dir := t.TempDir()

	err := expert.WriteState(dir, "Updated state content")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "state.md"))
	if err != nil {
		t.Fatalf("reading state.md: %v", err)
	}

	got := strings.TrimSpace(string(data))
	if got != "Updated state content" {
		t.Errorf("state.md = %q, want %q", got, "Updated state content")
	}
}

func TestWriteState_EmptyContent(t *testing.T) {
	dir := t.TempDir()

	err := expert.WriteState(dir, "")
	if err == nil {
		t.Fatal("expected error for empty content, got nil")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error = %q, want mention of 'empty'", err.Error())
	}
}

func TestWriteState_WhitespaceOnlyContent(t *testing.T) {
	dir := t.TempDir()

	err := expert.WriteState(dir, "   \n\t  ")
	if err == nil {
		t.Fatal("expected error for whitespace-only content, got nil")
	}
}

func TestWriteState_OversizedContent(t *testing.T) {
	dir := t.TempDir()

	big := strings.Repeat("x", expert.MaxStateSize+1)
	err := expert.WriteState(dir, big)
	if err == nil {
		t.Fatal("expected error for oversized content, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Errorf("error = %q, want mention of 'exceeds maximum'", err.Error())
	}
}

func TestWriteState_Overwrites(t *testing.T) {
	dir := t.TempDir()

	expert.WriteState(dir, "first version")
	expert.WriteState(dir, "second version")

	data, _ := os.ReadFile(filepath.Join(dir, "state.md"))
	got := strings.TrimSpace(string(data))
	if got != "second version" {
		t.Errorf("state.md = %q, want %q", got, "second version")
	}
}

func TestAppendError_HappyPath(t *testing.T) {
	dir := t.TempDir()

	err := expert.AppendError(dir, "JWT lib panics on empty string")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "errors.md"))
	if err != nil {
		t.Fatalf("reading errors.md: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "JWT lib panics on empty string") {
		t.Errorf("errors.md missing entry content")
	}
	if !strings.Contains(content, "###") {
		t.Errorf("errors.md missing timestamp header")
	}
}

func TestAppendError_AppendsToExisting(t *testing.T) {
	dir := t.TempDir()

	expert.AppendError(dir, "First error")
	expert.AppendError(dir, "Second error")

	data, _ := os.ReadFile(filepath.Join(dir, "errors.md"))
	content := string(data)

	if !strings.Contains(content, "First error") {
		t.Error("missing first error")
	}
	if !strings.Contains(content, "Second error") {
		t.Error("missing second error")
	}
	if strings.Count(content, "###") != 2 {
		t.Errorf("expected 2 timestamp headers, got %d", strings.Count(content, "###"))
	}
}

func TestAppendError_EmptyEntry(t *testing.T) {
	dir := t.TempDir()

	err := expert.AppendError(dir, "")
	if err == nil {
		t.Fatal("expected error for empty entry, got nil")
	}
}

func TestReadLog_HappyPath(t *testing.T) {
	dir := t.TempDir()

	logsDir := filepath.Join(dir, "logs")
	os.MkdirAll(logsDir, 0o755)
	os.WriteFile(filepath.Join(logsDir, "task-042.json"), []byte(`{"type":"result"}`), 0o644)

	data, err := expert.ReadLog(dir, "task-042")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(data) != `{"type":"result"}` {
		t.Errorf("got %q, want %q", string(data), `{"type":"result"}`)
	}
}

func TestReadLog_Missing(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "logs"), 0o755)

	_, err := expert.ReadLog(dir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing log, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want mention of 'not found'", err.Error())
	}
}

func TestReadLog_PathTraversal(t *testing.T) {
	dir := t.TempDir()

	_, err := expert.ReadLog(dir, "../../../etc/passwd")
	if err == nil {
		t.Fatal("expected error for path traversal, got nil")
	}
	if !strings.Contains(err.Error(), "invalid task ID") {
		t.Errorf("error = %q, want mention of 'invalid task ID'", err.Error())
	}
}

func TestReadLog_EmptyTaskID(t *testing.T) {
	dir := t.TempDir()

	_, err := expert.ReadLog(dir, "")
	if err == nil {
		t.Fatal("expected error for empty task ID, got nil")
	}
}

func TestSearchIndex_HappyPath(t *testing.T) {
	dir := t.TempDir()

	logsDir := filepath.Join(dir, "logs")
	os.MkdirAll(logsDir, 0o755)

	index := "| Task ID | Timestamp | From | Exit | Summary |\n" +
		"|---------|-----------|------|-----:|---------|\n" +
		"| task-001 | 2026-04-01T12:00:00Z | architect | 0 | Built auth endpoint |\n" +
		"| task-002 | 2026-04-02T12:00:00Z | architect | 0 | Fixed OAuth bug |\n" +
		"| task-003 | 2026-04-03T12:00:00Z | architect | 1 | Failed migration |\n"

	os.WriteFile(filepath.Join(logsDir, "index.md"), []byte(index), 0o644)

	matches, err := expert.SearchIndex(dir, "OAuth")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
	if !strings.Contains(matches[0], "task-002") {
		t.Errorf("match = %q, expected task-002 row", matches[0])
	}
}

func TestSearchIndex_CaseInsensitive(t *testing.T) {
	dir := t.TempDir()

	logsDir := filepath.Join(dir, "logs")
	os.MkdirAll(logsDir, 0o755)

	index := "| Task ID | Timestamp | From | Exit | Summary |\n" +
		"|---------|-----------|------|-----:|---------|\n" +
		"| task-001 | 2026-04-01T12:00:00Z | architect | 0 | Built AUTH endpoint |\n"

	os.WriteFile(filepath.Join(logsDir, "index.md"), []byte(index), 0o644)

	matches, err := expert.SearchIndex(dir, "auth")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matches))
	}
}

func TestSearchIndex_NoMatches(t *testing.T) {
	dir := t.TempDir()

	logsDir := filepath.Join(dir, "logs")
	os.MkdirAll(logsDir, 0o755)

	index := "| Task ID | Timestamp | From | Exit | Summary |\n" +
		"|---------|-----------|------|-----:|---------|\n" +
		"| task-001 | 2026-04-01T12:00:00Z | architect | 0 | Built auth endpoint |\n"

	os.WriteFile(filepath.Join(logsDir, "index.md"), []byte(index), 0o644)

	matches, err := expert.SearchIndex(dir, "nonexistent-query")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("expected 0 matches, got %d", len(matches))
	}
}

func TestSearchIndex_MissingIndex(t *testing.T) {
	dir := t.TempDir()

	matches, err := expert.SearchIndex(dir, "anything")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if matches != nil {
		t.Errorf("expected nil, got %v", matches)
	}
}
