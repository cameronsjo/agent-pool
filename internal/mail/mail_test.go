package mail_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.sjo.lol/cameron/agent-pool/internal/mail"
)

const validMessage = `---
id: task-042
from: architect
to: auth
type: task
contracts: [contract-007, contract-008]
priority: high
depends-on: [task-041]
timestamp: 2026-04-01T14:32:00Z
---

## Implement token endpoint

Build the OAuth token exchange endpoint per contract-007.
`

func TestParse_ValidMessage(t *testing.T) {
	msg, err := mail.Parse(validMessage, "test.md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg.ID != "task-042" {
		t.Errorf("ID = %q, want %q", msg.ID, "task-042")
	}
	if msg.From != "architect" {
		t.Errorf("From = %q, want %q", msg.From, "architect")
	}
	if msg.To != "auth" {
		t.Errorf("To = %q, want %q", msg.To, "auth")
	}
	if msg.Type != mail.TypeTask {
		t.Errorf("Type = %q, want %q", msg.Type, mail.TypeTask)
	}
	if msg.Priority != mail.PriorityHigh {
		t.Errorf("Priority = %q, want %q", msg.Priority, mail.PriorityHigh)
	}
	if len(msg.Contracts) != 2 || msg.Contracts[0] != "contract-007" {
		t.Errorf("Contracts = %v, want [contract-007 contract-008]", msg.Contracts)
	}
	if len(msg.DependsOn) != 1 || msg.DependsOn[0] != "task-041" {
		t.Errorf("DependsOn = %v, want [task-041]", msg.DependsOn)
	}
	expected := time.Date(2026, 4, 1, 14, 32, 0, 0, time.UTC)
	if !msg.Timestamp.Equal(expected) {
		t.Errorf("Timestamp = %v, want %v", msg.Timestamp, expected)
	}
	if msg.Body != "## Implement token endpoint\n\nBuild the OAuth token exchange endpoint per contract-007." {
		t.Errorf("Body = %q", msg.Body)
	}
	if msg.SourcePath != "test.md" {
		t.Errorf("SourcePath = %q, want %q", msg.SourcePath, "test.md")
	}
}

func TestParse_DefaultPriority(t *testing.T) {
	content := `---
id: q-001
from: concierge
to: auth
type: question
timestamp: 2026-04-01T14:32:00Z
---

What auth model applies here?
`
	msg, err := mail.Parse(content, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Priority != mail.PriorityNormal {
		t.Errorf("Priority = %q, want %q (default)", msg.Priority, mail.PriorityNormal)
	}
}

func TestParse_MissingRequiredField(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name: "missing id",
			content: `---
from: architect
to: auth
type: task
timestamp: 2026-04-01T14:32:00Z
---

Do something.
`,
			wantErr: "missing required field: id",
		},
		{
			name: "missing to",
			content: `---
id: task-001
from: architect
type: task
timestamp: 2026-04-01T14:32:00Z
---

Do something.
`,
			wantErr: "missing required field: to",
		},
		{
			name: "missing type",
			content: `---
id: task-001
from: architect
to: auth
timestamp: 2026-04-01T14:32:00Z
---

Do something.
`,
			wantErr: "missing required field: type",
		},
		{
			name: "id with path separator",
			content: `---
id: ../../etc/passwd
from: architect
to: auth
type: task
timestamp: 2026-04-01T14:32:00Z
---

Malicious.
`,
			wantErr: `invalid message ID "../../etc/passwd": must be a simple filename`,
		},
		{
			name: "id is dot",
			content: `---
id: .
from: architect
to: auth
type: task
timestamp: 2026-04-01T14:32:00Z
---

Dot.
`,
			wantErr: `invalid message ID ".": must be a simple filename`,
		},
		{
			name: "id is dot-dot",
			content: `---
id: ..
from: architect
to: auth
type: task
timestamp: 2026-04-01T14:32:00Z
---

DotDot.
`,
			wantErr: `invalid message ID "..": must be a simple filename`,
		},
		{
			name: "unknown message type",
			content: `---
id: task-001
from: architect
to: auth
type: taks
timestamp: 2026-04-01T14:32:00Z
---

Typo in type.
`,
			wantErr: `unknown message type "taks"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := mail.Parse(tt.content, "")
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if got := err.Error(); got != tt.wantErr {
				t.Errorf("error = %q, want %q", got, tt.wantErr)
			}
		})
	}
}

func TestParse_MalformedYAML(t *testing.T) {
	content := `---
id: [invalid yaml {{
---

Body.
`
	_, err := mail.Parse(content, "")
	if err == nil {
		t.Fatal("expected error for malformed YAML")
	}
}

func TestParse_NoFrontmatter(t *testing.T) {
	_, err := mail.Parse("Just plain text, no frontmatter.", "")
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestParse_EmptyBody(t *testing.T) {
	content := `---
id: task-001
from: architect
to: auth
type: task
timestamp: 2026-04-01T14:32:00Z
---
`
	msg, err := mail.Parse(content, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Body != "" {
		t.Errorf("Body = %q, want empty", msg.Body)
	}
}

func TestParseFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "task.md")
	if err := os.WriteFile(path, []byte(validMessage), 0o644); err != nil {
		t.Fatal(err)
	}

	msg, err := mail.ParseFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.ID != "task-042" {
		t.Errorf("ID = %q, want %q", msg.ID, "task-042")
	}
	if msg.SourcePath != path {
		t.Errorf("SourcePath = %q, want %q", msg.SourcePath, path)
	}
}

func TestParseFile_NotFound(t *testing.T) {
	_, err := mail.ParseFile("/nonexistent/path.md")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// Test Plan for mail.go
//
// Parse (Classification: INPUT PARSER)
//   [x] Happy: valid message with all fields (TestParse_ValidMessage)
//   [x] Happy: default priority when omitted (TestParse_DefaultPriority)
//   [x] Unhappy: missing each required field (TestParse_MissingRequiredField)
//   [x] Unhappy: malformed YAML (TestParse_MalformedYAML)
//   [x] Unhappy: no frontmatter (TestParse_NoFrontmatter)
//   [x] Boundary: empty body (TestParse_EmptyBody)
//   [x] Unhappy: missing from field (TestParse_MissingFrom)
//   [x] Boundary: leading whitespace before frontmatter (TestParse_LeadingWhitespace)
//   [x] Boundary: only opening delimiter (TestParse_OnlyOpeningDelimiter)
//   [x] Happy: unknown fields are ignored (TestParse_UnknownFieldsIgnored)
//   [x] Boundary: all message types parse correctly (TestParse_AllMessageTypes)
//   [x] Invalid: opening delimiter with trailing text (TestParse_DelimiterTrailingText)
//   [ ] Fuzz: Parse accepts arbitrary string input — candidate for go fuzzing
//
// ParseFile (Classification: I/O BOUNDARY)
//   [x] Happy: reads and parses file (TestParseFile)
//   [x] Unhappy: file not found (TestParseFile_NotFound)

func TestParse_MissingFrom(t *testing.T) {
	content := `---
id: task-001
to: auth
type: task
timestamp: 2026-04-01T14:32:00Z
---

Do something.
`
	_, err := mail.Parse(content, "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); got != "missing required field: from" {
		t.Errorf("error = %q, want %q", got, "missing required field: from")
	}
}

func TestParse_LeadingWhitespace(t *testing.T) {
	content := `
---
id: task-ws
from: architect
to: auth
type: task
timestamp: 2026-04-01T14:32:00Z
---

Body after whitespace.
`
	msg, err := mail.Parse(content, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.ID != "task-ws" {
		t.Errorf("ID = %q, want %q", msg.ID, "task-ws")
	}
}

func TestParse_OnlyOpeningDelimiter(t *testing.T) {
	content := `---
id: task-001
from: architect
to: auth
type: task
`
	_, err := mail.Parse(content, "")
	if err == nil {
		t.Fatal("expected error for missing closing delimiter")
	}
}

func TestParse_UnknownFieldsIgnored(t *testing.T) {
	content := `---
id: task-unk
from: architect
to: auth
type: task
timestamp: 2026-04-01T14:32:00Z
custom_field: should-be-ignored
another: 42
---

Body.
`
	msg, err := mail.Parse(content, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.ID != "task-unk" {
		t.Errorf("ID = %q, want %q", msg.ID, "task-unk")
	}
}

func TestParse_AllMessageTypes(t *testing.T) {
	types := []mail.MessageType{
		mail.TypeTask,
		mail.TypeQuestion,
		mail.TypeResponse,
		mail.TypeNotify,
		mail.TypeHandoff,
		mail.TypeCancel,
	}

	for _, mt := range types {
		t.Run(string(mt), func(t *testing.T) {
			content := "---\nid: msg-001\nfrom: a\nto: b\ntype: " + string(mt) + "\ntimestamp: 2026-04-01T14:32:00Z\n---\n\nBody.\n"
			msg, err := mail.Parse(content, "")
			if err != nil {
				t.Fatalf("unexpected error for type %q: %v", mt, err)
			}
			if msg.Type != mt {
				t.Errorf("Type = %q, want %q", msg.Type, mt)
			}
		})
	}
}

func TestParse_DelimiterTrailingText(t *testing.T) {
	// Opening --- followed by non-newline text should fail
	content := `--- not valid
id: task-001
from: a
to: b
type: task
---

Body.
`
	_, err := mail.Parse(content, "")
	if err == nil {
		t.Fatal("expected error for delimiter with trailing text")
	}
}

// FUZZ CANDIDATE: mail.Parse accepts untrusted string input (message files
// from the postoffice). Recommended: add to continuous fuzzing corpus with
// go test -fuzz. Harness: feed arbitrary strings, assert no panics, and that
// errors are returned (not panics) for all invalid inputs. Use race detector.
