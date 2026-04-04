package daemon_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cameronsjo/agent-pool/internal/daemon"
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

func TestWatcher_IgnoresRoutingTempFiles(t *testing.T) {
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

	// Write a .routing- temp file — should be ignored
	os.WriteFile(filepath.Join(dir, ".routing-abc123.md"), []byte("temp routing file"), 0o644)

	select {
	case event := <-w.Events():
		t.Errorf("unexpected event for .routing- temp file: %v", event)
	case <-time.After(1 * time.Second):
		// Expected — no event for .routing-* files
	}
}

func TestWatcher_MultipleDirectories(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	w, err := daemon.NewWatcher(logger)
	if err != nil {
		t.Fatalf("NewWatcher: %v", err)
	}
	defer w.Close()

	if err := w.Add(dir1); err != nil {
		t.Fatalf("Add dir1: %v", err)
	}
	if err := w.Add(dir2); err != nil {
		t.Fatalf("Add dir2: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go w.Run(ctx)

	time.Sleep(50 * time.Millisecond)

	absDir1, err := filepath.Abs(dir1)
	if err != nil {
		t.Fatalf("Abs dir1: %v", err)
	}
	absDir2, err := filepath.Abs(dir2)
	if err != nil {
		t.Fatalf("Abs dir2: %v", err)
	}

	// Write a .md file to dir1
	path1 := filepath.Join(dir1, "task-from-dir1.md")
	if err := os.WriteFile(path1, []byte("---\nid: task-dir1\n---\nFrom dir1.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case event := <-w.Events():
		if event.Dir != absDir1 {
			t.Errorf("event.Dir = %q, want %q", event.Dir, absDir1)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event from dir1")
	}

	// Write a .md file to dir2
	path2 := filepath.Join(dir2, "task-from-dir2.md")
	if err := os.WriteFile(path2, []byte("---\nid: task-dir2\n---\nFrom dir2.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case event := <-w.Events():
		if event.Dir != absDir2 {
			t.Errorf("event.Dir = %q, want %q", event.Dir, absDir2)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event from dir2")
	}
}

// Test Plan for watcher.go
//
// Watcher (Classification: STATE MACHINE + I/O BOUNDARY)
//   [x] Happy: detects new .md file (TestWatcher_DetectsNewMDFile)
//   [x] Behavioral: ignores non-.md files (TestWatcher_IgnoresNonMDFiles)
//   [x] Behavioral: ignores .routing-* temp files (TestWatcher_IgnoresRoutingTempFiles)
//   [x] Behavioral: multiple directories (TestWatcher_MultipleDirectories)
