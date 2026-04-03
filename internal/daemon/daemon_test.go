package daemon_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"git.sjo.lol/cameron/agent-pool/internal/config"
	"git.sjo.lol/cameron/agent-pool/internal/daemon"
)

func TestDaemon_EnsureDirsAndRouting(t *testing.T) {
	poolDir := t.TempDir()

	// Write a minimal pool.toml
	poolToml := `[pool]
name = "test-pool"
project_dir = "` + poolDir + `"

[experts.auth]
model = "sonnet"
`
	os.WriteFile(filepath.Join(poolDir, "pool.toml"), []byte(poolToml), 0o644)

	cfg, err := config.LoadPool(poolDir)
	if err != nil {
		t.Fatalf("LoadPool: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := daemon.New(cfg, poolDir, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Run daemon in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Run(ctx)
	}()

	// Give daemon time to start and create dirs
	time.Sleep(500 * time.Millisecond)

	// Verify directory structure was created
	for _, dir := range []string{
		filepath.Join(poolDir, "postoffice"),
		filepath.Join(poolDir, "experts", "auth", "inbox"),
		filepath.Join(poolDir, "experts", "auth", "logs"),
	} {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Errorf("expected directory %s to exist", dir)
		}
	}

	// Write a message to postoffice — it should get routed to auth's inbox
	msgContent := `---
id: task-routing-test
from: architect
to: auth
type: task
timestamp: 2026-04-01T14:32:00Z
---

Test routing.
`
	postPath := filepath.Join(poolDir, "postoffice", "task-routing-test.md")
	if err := os.WriteFile(postPath, []byte(msgContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Poll for the file to appear in auth's inbox
	inboxPath := filepath.Join(poolDir, "experts", "auth", "inbox", "task-routing-test.md")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(inboxPath); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if _, err := os.Stat(inboxPath); os.IsNotExist(err) {
		t.Error("message was not routed to auth inbox")
	}

	// Verify original was cleaned up from postoffice
	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Error("message should have been deleted from postoffice after routing")
	}

	// Shutdown
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("daemon returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("daemon did not shut down in time")
	}
}
