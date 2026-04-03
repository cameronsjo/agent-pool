package mail_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.sjo.lol/cameron/agent-pool/internal/mail"
)

func TestResolveInbox_BuiltinRoles(t *testing.T) {
	tests := []struct {
		recipient string
		want      string
	}{
		{"architect", "/pool/architect/inbox"},
		{"researcher", "/pool/researcher/inbox"},
		{"concierge", "/pool/concierge/inbox"},
	}

	for _, tt := range tests {
		got := mail.ResolveInbox("/pool", tt.recipient)
		if got != tt.want {
			t.Errorf("ResolveInbox(%q) = %q, want %q", tt.recipient, got, tt.want)
		}
	}
}

func TestResolveInbox_Experts(t *testing.T) {
	got := mail.ResolveInbox("/pool", "auth")
	want := "/pool/experts/auth/inbox"
	if got != want {
		t.Errorf("ResolveInbox(%q) = %q, want %q", "auth", got, want)
	}
}

func TestRoute_EndToEnd(t *testing.T) {
	poolDir := t.TempDir()

	// Set up directory structure
	postoffice := filepath.Join(poolDir, "postoffice")
	inbox := filepath.Join(poolDir, "experts", "auth", "inbox")
	for _, dir := range []string{postoffice, inbox} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Write a message to the postoffice
	msgContent := `---
id: task-099
from: architect
to: auth
type: task
timestamp: 2026-04-01T14:32:00Z
---

Do the thing.
`
	srcPath := filepath.Join(postoffice, "task-099.md")
	if err := os.WriteFile(srcPath, []byte(msgContent), 0o644); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	msg, err := mail.Route(logger, poolDir, srcPath)
	if err != nil {
		t.Fatalf("Route failed: %v", err)
	}

	if msg.ID != "task-099" {
		t.Errorf("msg.ID = %q, want %q", msg.ID, "task-099")
	}

	// Verify file appeared in inbox (named by message ID)
	destPath := filepath.Join(inbox, "task-099.md")
	if _, err := os.Stat(destPath); os.IsNotExist(err) {
		t.Error("routed file not found in inbox")
	}

	// Verify original was deleted
	if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
		t.Error("original file should have been deleted from postoffice")
	}
}

func TestRoute_UnknownRecipient(t *testing.T) {
	poolDir := t.TempDir()
	postoffice := filepath.Join(poolDir, "postoffice")
	os.MkdirAll(postoffice, 0o755)

	msgContent := `---
id: task-100
from: architect
to: nonexistent
type: task
timestamp: 2026-04-01T14:32:00Z
---

This should fail.
`
	srcPath := filepath.Join(postoffice, "task-100.md")
	os.WriteFile(srcPath, []byte(msgContent), 0o644)

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	_, err := mail.Route(logger, poolDir, srcPath)
	if err == nil {
		t.Fatal("expected error for unknown recipient")
	}
}

func TestRoute_IdempotentDelivery(t *testing.T) {
	poolDir := t.TempDir()

	postoffice := filepath.Join(poolDir, "postoffice")
	inbox := filepath.Join(poolDir, "experts", "auth", "inbox")
	for _, dir := range []string{postoffice, inbox} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	msgContent := `---
id: task-200
from: architect
to: auth
type: task
timestamp: 2026-04-01T15:00:00Z
---

Idempotent delivery test.
`

	// Write message to postoffice
	srcPath := filepath.Join(postoffice, "task-200.md")
	if err := os.WriteFile(srcPath, []byte(msgContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-write a file with the same message ID to the inbox (simulate prior delivery)
	destPath := filepath.Join(inbox, "task-200.md")
	if err := os.WriteFile(destPath, []byte("stale content"), 0o644); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	_, err := mail.Route(logger, poolDir, srcPath)
	if err != nil {
		t.Fatalf("Route failed on idempotent delivery: %v", err)
	}

	// Verify inbox file exists
	if _, err := os.Stat(destPath); os.IsNotExist(err) {
		t.Error("inbox file should exist after idempotent delivery")
	}

	// Verify original was deleted
	if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
		t.Error("original file should have been deleted from postoffice")
	}
}

func TestResolveInbox_EmptyRecipient(t *testing.T) {
	got := mail.ResolveInbox("/pool", "")
	want := "/pool/experts/inbox"
	if got != want {
		t.Errorf("ResolveInbox(%q) = %q, want %q", "", got, want)
	}
}

func TestRoute_OriginalPreservedOnCopyFailure(t *testing.T) {
	poolDir := t.TempDir()

	postoffice := filepath.Join(poolDir, "postoffice")
	if err := os.MkdirAll(postoffice, 0o755); err != nil {
		t.Fatal(err)
	}

	// Deliberately do NOT create the inbox directory for the recipient
	msgContent := `---
id: task-300
from: architect
to: auth
type: task
timestamp: 2026-04-01T16:00:00Z
---

This should fail and preserve the original.
`
	srcPath := filepath.Join(postoffice, "task-300.md")
	if err := os.WriteFile(srcPath, []byte(msgContent), 0o644); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	_, err := mail.Route(logger, poolDir, srcPath)
	if err == nil {
		t.Fatal("expected error when inbox directory does not exist")
	}

	// Verify original file is preserved
	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		t.Error("original file should be preserved in postoffice after routing failure")
	}
}

func TestRoute_UnreadableSourceFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test requires non-root")
	}

	poolDir := t.TempDir()

	postoffice := filepath.Join(poolDir, "postoffice")
	inbox := filepath.Join(poolDir, "experts", "auth", "inbox")
	for _, dir := range []string{postoffice, inbox} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	msgContent := `---
id: task-400
from: architect
to: auth
type: task
timestamp: 2026-04-01T17:00:00Z
---

Unreadable source test.
`
	srcPath := filepath.Join(postoffice, "task-400.md")
	if err := os.WriteFile(srcPath, []byte(msgContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Make the file unreadable
	if err := os.Chmod(srcPath, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Chmod(srcPath, 0o644)
	})

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	_, err := mail.Route(logger, poolDir, srcPath)
	if err == nil {
		t.Fatal("expected error when source file is unreadable")
	}
}

func TestRoute_ReadOnlyInboxDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test requires non-root")
	}

	poolDir := t.TempDir()

	postoffice := filepath.Join(poolDir, "postoffice")
	inbox := filepath.Join(poolDir, "experts", "auth", "inbox")
	for _, dir := range []string{postoffice, inbox} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	msgContent := `---
id: task-500
from: architect
to: auth
type: task
timestamp: 2026-04-01T18:00:00Z
---

Read-only inbox test.
`
	srcPath := filepath.Join(postoffice, "task-500.md")
	if err := os.WriteFile(srcPath, []byte(msgContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Make inbox read-only so file creation fails
	if err := os.Chmod(inbox, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		os.Chmod(inbox, 0o755)
	})

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	_, err := mail.Route(logger, poolDir, srcPath)
	if err == nil {
		t.Fatal("expected error when inbox directory is read-only")
	}

	if !strings.Contains(err.Error(), "copying") {
		t.Errorf("error should mention copying, got: %v", err)
	}
}

func TestRoute_ParseError(t *testing.T) {
	poolDir := t.TempDir()

	postoffice := filepath.Join(poolDir, "postoffice")
	if err := os.MkdirAll(postoffice, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write invalid content (no valid frontmatter)
	srcPath := filepath.Join(postoffice, "garbage.md")
	if err := os.WriteFile(srcPath, []byte("this is not valid frontmatter"), 0o644); err != nil {
		t.Fatal(err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	_, err := mail.Route(logger, poolDir, srcPath)
	if err == nil {
		t.Fatal("expected error for invalid message content")
	}

	if !strings.Contains(err.Error(), "parsing") {
		t.Errorf("error should mention parsing, got: %v", err)
	}

	// Verify the original file is NOT deleted
	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		t.Error("original file should be preserved when parsing fails")
	}
}

// Test Plan for router.go
//
// ResolveInbox (Classification: PURE LOGIC)
//   [x] Happy: builtin roles resolve correctly (TestResolveInbox_BuiltinRoles)
//   [x] Happy: experts resolve correctly (TestResolveInbox_Experts)
//   [x] Boundary: empty recipient (TestResolveInbox_EmptyRecipient)
//
// Route (Classification: I/O BOUNDARY)
//   [x] Happy: end-to-end routing (TestRoute_EndToEnd)
//   [x] Unhappy: unknown recipient (TestRoute_UnknownRecipient)
//   [x] Behavioral: idempotent delivery (TestRoute_IdempotentDelivery)
//   [x] Behavioral: original preserved on failure (TestRoute_OriginalPreservedOnCopyFailure)
//   [x] Unhappy: unreadable source file (TestRoute_UnreadableSourceFile)
//   [x] Unhappy: read-only inbox dir (TestRoute_ReadOnlyInboxDir)
//   [x] Unhappy: parse error in message (TestRoute_ParseError)
