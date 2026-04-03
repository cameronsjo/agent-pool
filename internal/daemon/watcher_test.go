package daemon_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.sjo.lol/cameron/agent-pool/internal/daemon"
)

func TestWatcher_DetectsNewMDFile(t *testing.T) {
	dir := t.TempDir()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	w, err := daemon.NewWatcher(logger)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	if err := w.Add(dir); err != nil {
		t.Fatalf("Add: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go w.Run(ctx)

	// Give watcher time to start
	time.Sleep(50 * time.Millisecond)

	// Write a .md file
	path := filepath.Join(dir, "task-001.md")
	content := `---
id: task-001
from: test
to: auth
type: task
timestamp: 2026-04-01T14:32:00Z
---

Test task.
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case event := <-w.Events():
		if event.Path != path {
			t.Errorf("event.Path = %q, want %q", event.Path, path)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for watcher event")
	}
}

func TestWatcher_IgnoresNonMDFiles(t *testing.T) {
	dir := t.TempDir()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	w, err := daemon.NewWatcher(logger)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	if err := w.Add(dir); err != nil {
		t.Fatalf("Add: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go w.Run(ctx)

	time.Sleep(50 * time.Millisecond)

	// Write a .txt file — should be ignored
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a mail"), 0o644)

	select {
	case event := <-w.Events():
		t.Errorf("unexpected event for non-.md file: %v", event)
	case <-time.After(1 * time.Second):
		// Expected — no event for .txt
	}
}
