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
