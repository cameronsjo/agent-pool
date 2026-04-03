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

	"git.sjo.lol/cameron/agent-pool/internal/hooks"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestFlush_RecentState(t *testing.T) {
	poolDir := t.TempDir()
	expertDir := filepath.Join(poolDir, "experts", "auth")
	os.MkdirAll(expertDir, 0o755)
	os.WriteFile(filepath.Join(expertDir, "state.md"), []byte("fresh state"), 0o644)

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
	os.MkdirAll(expertDir, 0o755)
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
	os.MkdirAll(expertDir, 0o755)

	statePath := filepath.Join(expertDir, "state.md")
	os.WriteFile(statePath, []byte("old state"), 0o644)

	// Set mtime to 2 hours ago
	oldTime := time.Now().Add(-2 * time.Hour)
	os.Chtimes(statePath, oldTime, oldTime)

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
