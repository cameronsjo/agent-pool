// Test plan for post.go:
//
// Post:
//   [x] Happy: message written to postoffice/{id}.md
//   [x] Happy: creates postoffice dir if missing
//   [x] File is valid parseable mail message

package mail

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPost_WritesMessage(t *testing.T) {
	poolDir := t.TempDir()

	msg := &Message{
		ID:        "task-post-001",
		From:      "daemon",
		To:        "researcher",
		Type:      TypeTask,
		Priority:  PriorityNormal,
		Timestamp: time.Now().UTC(),
		Body:      "Curate all experts.",
	}

	if err := Post(poolDir, msg); err != nil {
		t.Fatalf("Post: %v", err)
	}

	path := filepath.Join(poolDir, "postoffice", "task-post-001.md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("message file not created")
	}

	// Verify it's parseable
	parsed, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if parsed.ID != "task-post-001" {
		t.Errorf("ID = %q, want task-post-001", parsed.ID)
	}
	if parsed.To != "researcher" {
		t.Errorf("To = %q, want researcher", parsed.To)
	}
	if parsed.Body != "Curate all experts." {
		t.Errorf("Body = %q, want 'Curate all experts.'", parsed.Body)
	}
}

func TestPost_CreatesPostofficeDir(t *testing.T) {
	poolDir := t.TempDir()
	// Don't pre-create postoffice/

	msg := &Message{
		ID:        "task-mkdir-001",
		From:      "cli",
		To:        "researcher",
		Type:      TypeTask,
		Priority:  PriorityNormal,
		Timestamp: time.Now().UTC(),
		Body:      "Seed expert state.",
	}

	if err := Post(poolDir, msg); err != nil {
		t.Fatalf("Post: %v", err)
	}

	if _, err := os.Stat(filepath.Join(poolDir, "postoffice")); os.IsNotExist(err) {
		t.Error("postoffice dir not created")
	}
}
