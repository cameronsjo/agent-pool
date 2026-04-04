// Test plan for compose.go:
//
// Compose:
//   - Round-trip: Compose → Parse recovers same fields
//   - Missing required fields → error for each
//   - Nil message → error
//   - Invalid ID (path traversal, dot, slash) → error
//   - Default priority applied when empty
//   - Default timestamp applied when zero
//   - Body with special characters (pipes, dashes) survives round-trip
//   - Contracts and DependsOn omitted when empty
//   - Round-trip: cancel message with Cancels field
//   - Cancels omitted when empty

package mail_test

import (
	"strings"
	"testing"
	"time"

	"github.com/cameronsjo/agent-pool/internal/mail"
)

func TestCompose_RoundTrip(t *testing.T) {
	original := &mail.Message{
		ID:        "task-042",
		From:      "architect",
		To:        "auth",
		Type:      mail.TypeTask,
		Contracts: []string{"contract-007"},
		DependsOn: []string{"task-041"},
		Priority:  mail.PriorityHigh,
		Timestamp: time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC),
		Body:      "Build the token endpoint.",
	}

	composed, err := mail.Compose(original)
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}

	parsed, err := mail.Parse(composed, "test.md")
	if err != nil {
		t.Fatalf("Parse error: %v\n\nComposed:\n%s", err, composed)
	}

	if parsed.ID != original.ID {
		t.Errorf("ID = %q, want %q", parsed.ID, original.ID)
	}
	if parsed.From != original.From {
		t.Errorf("From = %q, want %q", parsed.From, original.From)
	}
	if parsed.To != original.To {
		t.Errorf("To = %q, want %q", parsed.To, original.To)
	}
	if parsed.Type != original.Type {
		t.Errorf("Type = %q, want %q", parsed.Type, original.Type)
	}
	if parsed.Priority != original.Priority {
		t.Errorf("Priority = %q, want %q", parsed.Priority, original.Priority)
	}
	if parsed.Body != original.Body {
		t.Errorf("Body = %q, want %q", parsed.Body, original.Body)
	}
	if len(parsed.Contracts) != 1 || parsed.Contracts[0] != "contract-007" {
		t.Errorf("Contracts = %v, want [contract-007]", parsed.Contracts)
	}
	if len(parsed.DependsOn) != 1 || parsed.DependsOn[0] != "task-041" {
		t.Errorf("DependsOn = %v, want [task-041]", parsed.DependsOn)
	}
}

func TestCompose_RoundTripResponse(t *testing.T) {
	original := &mail.Message{
		ID:        "resp-001",
		From:      "auth",
		To:        "architect",
		Type:      mail.TypeResponse,
		Priority:  mail.PriorityNormal,
		Timestamp: time.Date(2026, 4, 3, 14, 30, 0, 0, time.UTC),
		Body:      "Token endpoint is complete.\n\n## Details\n\n- OAuth2 flows implemented\n- JWT signing with RS256",
	}

	composed, err := mail.Compose(original)
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}

	parsed, err := mail.Parse(composed, "test.md")
	if err != nil {
		t.Fatalf("Parse error: %v\n\nComposed:\n%s", err, composed)
	}

	if parsed.Body != original.Body {
		t.Errorf("Body mismatch.\nGot:  %q\nWant: %q", parsed.Body, original.Body)
	}
}

func TestCompose_MissingID(t *testing.T) {
	msg := &mail.Message{From: "a", To: "b", Type: mail.TypeTask}
	_, err := mail.Compose(msg)
	if err == nil {
		t.Fatal("expected error for missing ID")
	}
}

func TestCompose_MissingFrom(t *testing.T) {
	msg := &mail.Message{ID: "x", To: "b", Type: mail.TypeTask}
	_, err := mail.Compose(msg)
	if err == nil {
		t.Fatal("expected error for missing From")
	}
}

func TestCompose_MissingTo(t *testing.T) {
	msg := &mail.Message{ID: "x", From: "a", Type: mail.TypeTask}
	_, err := mail.Compose(msg)
	if err == nil {
		t.Fatal("expected error for missing To")
	}
}

func TestCompose_MissingType(t *testing.T) {
	msg := &mail.Message{ID: "x", From: "a", To: "b"}
	_, err := mail.Compose(msg)
	if err == nil {
		t.Fatal("expected error for missing Type")
	}
}

func TestCompose_NilMessage(t *testing.T) {
	_, err := mail.Compose(nil)
	if err == nil {
		t.Fatal("expected error for nil message")
	}
}

func TestCompose_DefaultPriority(t *testing.T) {
	msg := &mail.Message{
		ID:        "task-001",
		From:      "architect",
		To:        "auth",
		Type:      mail.TypeTask,
		Timestamp: time.Now().UTC(),
		Body:      "Do something",
	}

	composed, err := mail.Compose(msg)
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}

	parsed, err := mail.Parse(composed, "test.md")
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}

	if parsed.Priority != mail.PriorityNormal {
		t.Errorf("Priority = %q, want %q", parsed.Priority, mail.PriorityNormal)
	}
}

func TestCompose_BodyWithSpecialChars(t *testing.T) {
	msg := &mail.Message{
		ID:        "task-001",
		From:      "architect",
		To:        "auth",
		Type:      mail.TypeTask,
		Priority:  mail.PriorityNormal,
		Timestamp: time.Now().UTC(),
		Body:      "Table: | col1 | col2 |\n---\nDashes everywhere",
	}

	composed, err := mail.Compose(msg)
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}

	parsed, err := mail.Parse(composed, "test.md")
	if err != nil {
		t.Fatalf("Parse error: %v\n\nComposed:\n%s", err, composed)
	}

	if parsed.Body != msg.Body {
		t.Errorf("Body mismatch.\nGot:  %q\nWant: %q", parsed.Body, msg.Body)
	}
}

func TestCompose_RoundTripCancel(t *testing.T) {
	original := &mail.Message{
		ID:        "cancel-001",
		From:      "architect",
		To:        "auth",
		Type:      mail.TypeCancel,
		Cancels:   "task-042",
		Priority:  mail.PriorityNormal,
		Timestamp: time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC),
		Body:      "Cancel the token endpoint work.",
	}

	composed, err := mail.Compose(original)
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}

	parsed, err := mail.Parse(composed, "test.md")
	if err != nil {
		t.Fatalf("Parse error: %v\n\nComposed:\n%s", err, composed)
	}

	if parsed.Type != mail.TypeCancel {
		t.Errorf("Type = %q, want %q", parsed.Type, mail.TypeCancel)
	}
	if parsed.Cancels != original.Cancels {
		t.Errorf("Cancels = %q, want %q", parsed.Cancels, original.Cancels)
	}
	if parsed.Body != original.Body {
		t.Errorf("Body = %q, want %q", parsed.Body, original.Body)
	}
}

func TestCompose_OmitsCancelsWhenEmpty(t *testing.T) {
	msg := &mail.Message{
		ID:        "task-001",
		From:      "architect",
		To:        "auth",
		Type:      mail.TypeTask,
		Priority:  mail.PriorityNormal,
		Timestamp: time.Now().UTC(),
		Body:      "Simple task",
	}

	composed, err := mail.Compose(msg)
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}

	if strings.Contains(composed, "cancels") {
		t.Errorf("composed output should omit empty cancels field:\n%s", composed)
	}
}

func TestCompose_InvalidID(t *testing.T) {
	tests := []struct {
		name string
		id   string
	}{
		{"path separator", "../../etc/passwd"},
		{"dot", "."},
		{"dot-dot", ".."},
		{"slash", "foo/bar"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := &mail.Message{
				ID:   tc.id,
				From: "arch",
				To:   "auth",
				Type: mail.TypeTask,
				Body: "test",
			}
			_, err := mail.Compose(msg)
			if err == nil {
				t.Errorf("Compose(%q) should return error", tc.id)
			}
		})
	}
}

func TestCompose_OmitsEmptyCollections(t *testing.T) {
	msg := &mail.Message{
		ID:        "task-001",
		From:      "architect",
		To:        "auth",
		Type:      mail.TypeTask,
		Priority:  mail.PriorityNormal,
		Timestamp: time.Now().UTC(),
		Body:      "Simple task",
	}

	composed, err := mail.Compose(msg)
	if err != nil {
		t.Fatalf("Compose error: %v", err)
	}

	if strings.Contains(composed, "contracts") {
		t.Errorf("composed output should omit empty contracts field:\n%s", composed)
	}
	if strings.Contains(composed, "depends-on") {
		t.Errorf("composed output should omit empty depends-on field:\n%s", composed)
	}
}
