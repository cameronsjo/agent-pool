// Test plan for flush.go:
//
// Flush:
//   - state.md recently modified → info log, no error
//   - state.md missing → warn log, no error
//   - state.md stale (old mtime) → warn log, no error
//   - Empty pool dir → error
//   - Empty expert name → error

package hooks_test

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cameronsjo/agent-pool/internal/hooks"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestFlush_RecentState(t *testing.T) {
	poolDir := t.TempDir()
	expertDir := filepath.Join(poolDir, "experts", "auth")
	if err := os.MkdirAll(expertDir, 0o755); err != nil {
		t.Fatalf("creating expert dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(expertDir, "state.md"), []byte("fresh state"), 0o644); err != nil {
		t.Fatalf("writing state.md: %v", err)
	}

	cfg := &hooks.FlushConfig{
		PoolDir:    poolDir,
		ExpertName: "auth",
		TaskID:     "task-001",
	}

	err := hooks.Flush(testLogger(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFlush_MissingState(t *testing.T) {
	poolDir := t.TempDir()
	expertDir := filepath.Join(poolDir, "experts", "auth")
	if err := os.MkdirAll(expertDir, 0o755); err != nil {
		t.Fatalf("creating expert dir: %v", err)
	}
	// No state.md

	cfg := &hooks.FlushConfig{
		PoolDir:    poolDir,
		ExpertName: "auth",
		TaskID:     "task-001",
	}

	err := hooks.Flush(testLogger(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFlush_StaleState(t *testing.T) {
	poolDir := t.TempDir()
	expertDir := filepath.Join(poolDir, "experts", "auth")
	if err := os.MkdirAll(expertDir, 0o755); err != nil {
		t.Fatalf("creating expert dir: %v", err)
	}

	statePath := filepath.Join(expertDir, "state.md")
	if err := os.WriteFile(statePath, []byte("old state"), 0o644); err != nil {
		t.Fatalf("writing state.md: %v", err)
	}

	// Set mtime to 2 hours ago
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(statePath, oldTime, oldTime); err != nil {
		t.Fatalf("setting mtime: %v", err)
	}

	cfg := &hooks.FlushConfig{
		PoolDir:    poolDir,
		ExpertName: "auth",
		TaskID:     "task-001",
	}

	err := hooks.Flush(testLogger(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFlush_EmptyPoolDir(t *testing.T) {
	cfg := &hooks.FlushConfig{
		ExpertName: "auth",
		TaskID:     "task-001",
	}

	err := hooks.Flush(testLogger(), cfg)
	if err == nil {
		t.Fatal("expected error for empty pool dir")
	}
	if !strings.Contains(err.Error(), "pool directory") {
		t.Errorf("error = %q, want mention of 'pool directory'", err.Error())
	}
}

func TestFlush_EmptyExpertName(t *testing.T) {
	cfg := &hooks.FlushConfig{
		PoolDir: "/tmp",
		TaskID:  "task-001",
	}

	err := hooks.Flush(testLogger(), cfg)
	if err == nil {
		t.Fatal("expected error for empty expert name")
	}
}
