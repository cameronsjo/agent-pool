package daemon_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"encoding/json"

	"git.sjo.lol/cameron/agent-pool/internal/config"
	"git.sjo.lol/cameron/agent-pool/internal/daemon"
	"git.sjo.lol/cameron/agent-pool/internal/expert"
	"git.sjo.lol/cameron/agent-pool/internal/taskboard"
)

// fakeSpawner records calls and returns canned results.
type fakeSpawner struct {
	mu    sync.Mutex
	calls []*expert.SpawnConfig

	// result and err are returned from Spawn. Defaults to a zero-value Result if nil.
	result *expert.Result
	err    error

	// If set, Spawn blocks on this channel before returning.
	gate chan struct{}
}

func (f *fakeSpawner) Spawn(_ context.Context, _ *slog.Logger, cfg *expert.SpawnConfig) (*expert.Result, error) {
	if f.gate != nil {
		<-f.gate
	}

	f.mu.Lock()
	f.calls = append(f.calls, cfg)
	f.mu.Unlock()

	if f.err != nil {
		return nil, f.err
	}

	r := f.result
	if r == nil {
		r = &expert.Result{
			TaskID:   cfg.TaskMessage.ID,
			ExitCode: 0,
			Output:   []byte(`{"type":"result","result":"done"}`),
			Summary:  "Task completed",
			Duration: 100 * time.Millisecond,
		}
	}
	return r, nil
}

func (f *fakeSpawner) getCalls() []*expert.SpawnConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*expert.SpawnConfig, len(f.calls))
	copy(out, f.calls)
	return out
}

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
	d := daemon.New(cfg, poolDir, logger, daemon.WithSpawner(&fakeSpawner{}))

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

	// Poll for evidence of processing — the log file is the durable artifact.
	// The inbox file is transient (removed on success), so polling for it is
	// racy when expert dispatch runs in a goroutine.
	logPath := filepath.Join(poolDir, "experts", "auth", "logs", "task-routing-test.json")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(logPath); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("message was not routed and processed (log file missing)")
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

// writeMessage writes a YAML-frontmatter mail file to the given directory.
func writeMessage(t *testing.T, dir, id, from, to string) string {
	t.Helper()
	content := fmt.Sprintf(`---
id: %s
from: %s
to: %s
type: task
timestamp: 2026-04-01T14:32:00Z
---

Task body for %s.
`, id, from, to, id)
	path := filepath.Join(dir, id+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing message %s: %v", id, err)
	}
	return path
}

// shutdownDaemon cancels the context and waits for the daemon to exit.
func shutdownDaemon(t *testing.T, cancel context.CancelFunc, errCh <-chan error) {
	t.Helper()
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

func TestDaemon_HandleInboxWithFakeSpawner(t *testing.T) {
	poolDir := t.TempDir()

	poolToml := `[pool]
name = "test-pool"
project_dir = "` + poolDir + `"

[defaults]
model = "sonnet"

[experts.auth]
`
	os.WriteFile(filepath.Join(poolDir, "pool.toml"), []byte(poolToml), 0o644)

	cfg, err := config.LoadPool(poolDir)
	if err != nil {
		t.Fatalf("LoadPool: %v", err)
	}

	fake := &fakeSpawner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := daemon.New(cfg, poolDir, logger, daemon.WithSpawner(fake))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	// Wait for dirs to be created
	time.Sleep(500 * time.Millisecond)

	// Write a message directly to auth's inbox
	inboxDir := filepath.Join(poolDir, "experts", "auth", "inbox")
	writeMessage(t, inboxDir, "task-fake-001", "architect", "auth")

	// Poll for spawn call
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(fake.getCalls()) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	calls := fake.getCalls()
	if len(calls) == 0 {
		t.Fatal("expected fakeSpawner to be called, got 0 calls")
	}
	if calls[0].Name != "auth" {
		t.Errorf("expected expert name 'auth', got %q", calls[0].Name)
	}
	if calls[0].TaskMessage.ID != "task-fake-001" {
		t.Errorf("expected task ID 'task-fake-001', got %q", calls[0].TaskMessage.ID)
	}

	// Verify log file was written
	logPath := filepath.Join(poolDir, "experts", "auth", "logs", "task-fake-001.json")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("expected log file to be written")
	}

	// Verify inbox file was cleaned up
	inboxFile := filepath.Join(inboxDir, "task-fake-001.md")
	if _, err := os.Stat(inboxFile); !os.IsNotExist(err) {
		t.Error("expected inbox file to be removed after processing")
	}

	shutdownDaemon(t, cancel, errCh)
}

func TestDaemon_ExpertBusyQueuesMessage(t *testing.T) {
	poolDir := t.TempDir()

	poolToml := `[pool]
name = "test-pool"
project_dir = "` + poolDir + `"

[defaults]
model = "sonnet"

[experts.auth]
`
	os.WriteFile(filepath.Join(poolDir, "pool.toml"), []byte(poolToml), 0o644)

	cfg, err := config.LoadPool(poolDir)
	if err != nil {
		t.Fatalf("LoadPool: %v", err)
	}

	gate := make(chan struct{})
	fake := &fakeSpawner{gate: gate}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := daemon.New(cfg, poolDir, logger, daemon.WithSpawner(fake))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	time.Sleep(500 * time.Millisecond)

	inboxDir := filepath.Join(poolDir, "experts", "auth", "inbox")

	// Drop first message — it will block on gate
	writeMessage(t, inboxDir, "task-busy-001", "architect", "auth")

	// Wait briefly for the first spawn to start blocking
	time.Sleep(500 * time.Millisecond)

	// Drop second message while expert is busy
	writeMessage(t, inboxDir, "task-busy-002", "architect", "auth")

	// Wait for the second message to be noticed as queued
	time.Sleep(500 * time.Millisecond)

	// At this point only 0 calls should have completed (first is blocked)
	if len(fake.getCalls()) != 0 {
		t.Fatalf("expected 0 completed calls while gate is closed, got %d", len(fake.getCalls()))
	}

	// Unblock first spawn
	close(gate)

	// Poll until both spawns complete
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(fake.getCalls()) >= 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	calls := fake.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 spawn calls, got %d", len(calls))
	}

	// Verify both tasks were processed
	ids := map[string]bool{}
	for _, c := range calls {
		ids[c.TaskMessage.ID] = true
	}
	if !ids["task-busy-001"] || !ids["task-busy-002"] {
		t.Errorf("expected both task IDs to be processed, got %v", ids)
	}

	shutdownDaemon(t, cancel, errCh)
}

func TestDaemon_ResolveExpertConfig(t *testing.T) {
	poolDir := t.TempDir()

	poolToml := `[pool]
name = "test-pool"
project_dir = "` + poolDir + `"

[defaults]
model = "sonnet"

[experts.auth]
model = "opus"

[experts.api]
`
	os.WriteFile(filepath.Join(poolDir, "pool.toml"), []byte(poolToml), 0o644)

	cfg, err := config.LoadPool(poolDir)
	if err != nil {
		t.Fatalf("LoadPool: %v", err)
	}

	fake := &fakeSpawner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := daemon.New(cfg, poolDir, logger, daemon.WithSpawner(fake))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	time.Sleep(500 * time.Millisecond)

	// Send task to auth (custom model: opus)
	authInbox := filepath.Join(poolDir, "experts", "auth", "inbox")
	writeMessage(t, authInbox, "task-model-auth", "architect", "auth")

	// Wait for auth to be processed
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(fake.getCalls()) >= 1 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Send task to api (default model: sonnet)
	apiInbox := filepath.Join(poolDir, "experts", "api", "inbox")
	writeMessage(t, apiInbox, "task-model-api", "architect", "api")

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(fake.getCalls()) >= 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	calls := fake.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 spawn calls, got %d", len(calls))
	}

	// Find each call by expert name and verify model
	for _, c := range calls {
		switch c.Name {
		case "auth":
			if c.Model != "opus" {
				t.Errorf("expected auth model 'opus', got %q", c.Model)
			}
		case "api":
			if c.Model != "sonnet" {
				t.Errorf("expected api model 'sonnet' (pool default), got %q", c.Model)
			}
		default:
			t.Errorf("unexpected expert name: %q", c.Name)
		}
	}

	shutdownDaemon(t, cancel, errCh)
}

func TestDaemon_NonZeroExitPreservesInbox(t *testing.T) {
	poolDir := t.TempDir()

	poolToml := `[pool]
name = "test-pool"
project_dir = "` + poolDir + `"

[defaults]
model = "sonnet"

[experts.auth]
`
	os.WriteFile(filepath.Join(poolDir, "pool.toml"), []byte(poolToml), 0o644)

	cfg, err := config.LoadPool(poolDir)
	if err != nil {
		t.Fatalf("LoadPool: %v", err)
	}

	fake := &fakeSpawner{
		result: &expert.Result{
			TaskID:   "task-fail-001",
			ExitCode: 1,
			Output:   []byte(`{"type":"result","result":"something went wrong"}`),
			Summary:  "Expert errored out",
			Duration: 50 * time.Millisecond,
		},
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := daemon.New(cfg, poolDir, logger, daemon.WithSpawner(fake))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	time.Sleep(500 * time.Millisecond)

	inboxDir := filepath.Join(poolDir, "experts", "auth", "inbox")
	writeMessage(t, inboxDir, "task-fail-001", "architect", "auth")

	// Poll for spawn call
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(fake.getCalls()) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	calls := fake.getCalls()
	if len(calls) == 0 {
		t.Fatal("expected fakeSpawner to be called")
	}

	// Allow time for post-spawn processing
	time.Sleep(500 * time.Millisecond)

	// Log file SHOULD be written even on non-zero exit
	logPath := filepath.Join(poolDir, "experts", "auth", "logs", "task-fail-001.json")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("expected log file to be written even on non-zero exit")
	}

	// Inbox file should NOT be removed on non-zero exit
	inboxFile := filepath.Join(inboxDir, "task-fail-001.md")
	if _, err := os.Stat(inboxFile); os.IsNotExist(err) {
		t.Error("inbox file should be preserved when exit code is non-zero")
	}

	shutdownDaemon(t, cancel, errCh)
}

func TestDaemon_DrainPostoffice(t *testing.T) {
	poolDir := t.TempDir()

	poolToml := `[pool]
name = "test-pool"
project_dir = "` + poolDir + `"

[defaults]
model = "sonnet"

[experts.auth]
`
	os.WriteFile(filepath.Join(poolDir, "pool.toml"), []byte(poolToml), 0o644)

	cfg, err := config.LoadPool(poolDir)
	if err != nil {
		t.Fatalf("LoadPool: %v", err)
	}

	// Create directories manually BEFORE starting daemon, so we can drop a
	// message into the postoffice before the daemon's ensureDirs runs.
	postofficeDir := filepath.Join(poolDir, "postoffice")
	os.MkdirAll(postofficeDir, 0o755)
	inboxDir := filepath.Join(poolDir, "experts", "auth", "inbox")
	os.MkdirAll(inboxDir, 0o755)
	os.MkdirAll(filepath.Join(poolDir, "experts", "auth", "logs"), 0o755)

	// Write a message to postoffice BEFORE the daemon starts
	msgContent := `---
id: task-pre-existing
from: architect
to: auth
type: task
timestamp: 2026-04-01T14:32:00Z
---

Pre-existing postoffice message.
`
	postPath := filepath.Join(postofficeDir, "task-pre-existing.md")
	os.WriteFile(postPath, []byte(msgContent), 0o644)

	fake := &fakeSpawner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := daemon.New(cfg, poolDir, logger, daemon.WithSpawner(fake))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	// Poll for the message to be routed to auth's inbox AND processed by spawn
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(fake.getCalls()) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	calls := fake.getCalls()
	if len(calls) == 0 {
		t.Fatal("expected pre-existing postoffice message to be routed and spawned")
	}
	if calls[0].TaskMessage.ID != "task-pre-existing" {
		t.Errorf("expected task ID 'task-pre-existing', got %q", calls[0].TaskMessage.ID)
	}

	// Verify original was cleaned up from postoffice
	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Error("message should have been deleted from postoffice after routing")
	}

	shutdownDaemon(t, cancel, errCh)
}

// loadTaskboard reads the taskboard.json file from a pool directory.
func loadTaskboard(t *testing.T, poolDir string) *taskboard.Board {
	t.Helper()
	path := filepath.Join(poolDir, "taskboard.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading taskboard.json: %v", err)
	}
	var b taskboard.Board
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatalf("parsing taskboard.json: %v", err)
	}
	return &b
}

func TestDaemon_TaskboardCreatedOnStart(t *testing.T) {
	poolDir := t.TempDir()

	poolToml := `[pool]
name = "test-pool"
project_dir = "` + poolDir + `"

[experts.auth]
`
	os.WriteFile(filepath.Join(poolDir, "pool.toml"), []byte(poolToml), 0o644)

	cfg, _ := config.LoadPool(poolDir)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := daemon.New(cfg, poolDir, logger, daemon.WithSpawner(&fakeSpawner{}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	time.Sleep(500 * time.Millisecond)

	// Write a task to postoffice so the taskboard gets written
	writeMessage(t, filepath.Join(poolDir, "postoffice"), "task-tb-001", "architect", "auth")

	// Poll for processing
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(poolDir, "taskboard.json")); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	board := loadTaskboard(t, poolDir)
	task, ok := board.Tasks["task-tb-001"]
	if !ok {
		t.Fatal("task-tb-001 not found in taskboard")
	}
	if task.Expert != "auth" {
		t.Errorf("Expert = %q, want %q", task.Expert, "auth")
	}

	shutdownDaemon(t, cancel, errCh)
}

func TestDaemon_TaskStatusLifecycle(t *testing.T) {
	poolDir := t.TempDir()

	poolToml := `[pool]
name = "test-pool"
project_dir = "` + poolDir + `"

[experts.auth]
`
	os.WriteFile(filepath.Join(poolDir, "pool.toml"), []byte(poolToml), 0o644)

	cfg, _ := config.LoadPool(poolDir)
	fake := &fakeSpawner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := daemon.New(cfg, poolDir, logger, daemon.WithSpawner(fake))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	time.Sleep(500 * time.Millisecond)

	// Route a task through the postoffice
	writeMessage(t, filepath.Join(poolDir, "postoffice"), "task-lifecycle", "architect", "auth")

	// Poll for completion
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(fake.getCalls()) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Allow post-spawn processing
	time.Sleep(500 * time.Millisecond)

	board := loadTaskboard(t, poolDir)
	task, ok := board.Tasks["task-lifecycle"]
	if !ok {
		t.Fatal("task-lifecycle not found in taskboard")
	}
	if task.Status != "completed" {
		t.Errorf("Status = %q, want %q", task.Status, "completed")
	}
	if task.ExitCode == nil || *task.ExitCode != 0 {
		t.Errorf("ExitCode = %v, want 0", task.ExitCode)
	}
	if task.StartedAt == nil {
		t.Error("StartedAt should be set")
	}
	if task.CompletedAt == nil {
		t.Error("CompletedAt should be set")
	}

	shutdownDaemon(t, cancel, errCh)
}

func TestDaemon_TaskFailedTrackedInTaskboard(t *testing.T) {
	poolDir := t.TempDir()

	poolToml := `[pool]
name = "test-pool"
project_dir = "` + poolDir + `"

[experts.auth]
`
	os.WriteFile(filepath.Join(poolDir, "pool.toml"), []byte(poolToml), 0o644)

	cfg, _ := config.LoadPool(poolDir)
	fake := &fakeSpawner{
		result: &expert.Result{
			TaskID:   "task-fail-tracked",
			ExitCode: 1,
			Output:   []byte(`{"type":"result","result":"error"}`),
			Summary:  "Failed",
			Duration: 50 * time.Millisecond,
		},
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := daemon.New(cfg, poolDir, logger, daemon.WithSpawner(fake))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	time.Sleep(500 * time.Millisecond)

	writeMessage(t, filepath.Join(poolDir, "postoffice"), "task-fail-tracked", "architect", "auth")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(fake.getCalls()) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	time.Sleep(500 * time.Millisecond)

	board := loadTaskboard(t, poolDir)
	task, ok := board.Tasks["task-fail-tracked"]
	if !ok {
		t.Fatal("task-fail-tracked not found in taskboard")
	}
	if task.Status != "failed" {
		t.Errorf("Status = %q, want %q", task.Status, "failed")
	}
	if task.ExitCode == nil || *task.ExitCode != 1 {
		t.Errorf("ExitCode = %v, want 1", task.ExitCode)
	}

	shutdownDaemon(t, cancel, errCh)
}

// writeCancelMessage writes a cancel mail file targeting a specific task.
func writeCancelMessage(t *testing.T, dir, cancelID, targetID, from, to string) string {
	t.Helper()
	content := fmt.Sprintf(`---
id: %s
from: %s
to: %s
type: cancel
cancels: %s
timestamp: 2026-04-01T15:00:00Z
---

Cancel %s.
`, cancelID, from, to, targetID, targetID)
	path := filepath.Join(dir, cancelID+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing cancel message %s: %v", cancelID, err)
	}
	return path
}

func TestDaemon_CancelPendingTask(t *testing.T) {
	poolDir := t.TempDir()

	poolToml := `[pool]
name = "test-pool"
project_dir = "` + poolDir + `"

[experts.auth]
`
	os.WriteFile(filepath.Join(poolDir, "pool.toml"), []byte(poolToml), 0o644)

	cfg, _ := config.LoadPool(poolDir)
	fake := &fakeSpawner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := daemon.New(cfg, poolDir, logger, daemon.WithSpawner(fake))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	time.Sleep(500 * time.Millisecond)

	postDir := filepath.Join(poolDir, "postoffice")

	// Route a blocked task (has dependency on non-existent task) so it stays
	// in the inbox without being spawned.
	blockedContent := `---
id: task-cancel-target
from: architect
to: auth
type: task
depends-on: [task-prereq-999]
timestamp: 2026-04-01T14:32:00Z
---

This task is blocked and will be cancelled.
`
	os.WriteFile(filepath.Join(postDir, "task-cancel-target.md"), []byte(blockedContent), 0o644)

	// Wait for the task to be routed to inbox
	inboxFile := filepath.Join(poolDir, "experts", "auth", "inbox", "task-cancel-target.md")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(inboxFile); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Wait for taskboard to reflect blocked status
	time.Sleep(500 * time.Millisecond)

	// Now send a cancel message
	writeCancelMessage(t, postDir, "cancel-001", "task-cancel-target", "architect", "auth")

	// Wait for cancel to be processed (cancel file removed from postoffice)
	cancelPostPath := filepath.Join(postDir, "cancel-001.md")
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(cancelPostPath); os.IsNotExist(err) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Allow processing
	time.Sleep(500 * time.Millisecond)

	// Verify: taskboard shows cancelled
	board := loadTaskboard(t, poolDir)
	task, ok := board.Tasks["task-cancel-target"]
	if !ok {
		t.Fatal("task-cancel-target not found in taskboard")
	}
	if task.Status != "cancelled" {
		t.Errorf("Status = %q, want %q", task.Status, "cancelled")
	}

	// Verify: inbox file was removed by the cancel handler
	if _, err := os.Stat(inboxFile); !os.IsNotExist(err) {
		t.Error("inbox file should have been removed after cancellation")
	}

	// Verify: expert was never spawned for the cancelled task
	for _, c := range fake.getCalls() {
		if c.TaskMessage.ID == "task-cancel-target" {
			t.Error("expert should not have been spawned for cancelled task")
		}
	}

	shutdownDaemon(t, cancel, errCh)
}

func TestDaemon_CancelActiveTask(t *testing.T) {
	poolDir := t.TempDir()

	poolToml := `[pool]
name = "test-pool"
project_dir = "` + poolDir + `"

[experts.auth]
`
	os.WriteFile(filepath.Join(poolDir, "pool.toml"), []byte(poolToml), 0o644)

	cfg, _ := config.LoadPool(poolDir)
	gate := make(chan struct{})
	fake := &fakeSpawner{gate: gate}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := daemon.New(cfg, poolDir, logger, daemon.WithSpawner(fake))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	time.Sleep(500 * time.Millisecond)

	postDir := filepath.Join(poolDir, "postoffice")

	// Route a task — it will be picked up and the spawner will block on gate
	writeMessage(t, postDir, "task-active-cancel", "architect", "auth")

	// Wait for the task to reach active status in the taskboard
	boardPath := filepath.Join(poolDir, "taskboard.json")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(boardPath)
		if err == nil {
			var b taskboard.Board
			if json.Unmarshal(data, &b) == nil {
				if task, ok := b.Tasks["task-active-cancel"]; ok && task.Status == "active" {
					break
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Send cancel while the task is active (spawner blocked on gate)
	writeCancelMessage(t, postDir, "cancel-active-001", "task-active-cancel", "architect", "auth")

	// Wait for cancel message to be consumed
	cancelPostPath := filepath.Join(postDir, "cancel-active-001.md")
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(cancelPostPath); os.IsNotExist(err) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	time.Sleep(200 * time.Millisecond)

	// Ungate — let the spawner complete
	close(gate)

	// Wait for spawn to complete
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(fake.getCalls()) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	time.Sleep(500 * time.Millisecond)

	// Verify: expert completed (cancel is a no-op for active)
	calls := fake.getCalls()
	if len(calls) == 0 {
		t.Fatal("expected spawner to be called for active task")
	}
	if calls[0].TaskMessage.ID != "task-active-cancel" {
		t.Errorf("expected task ID 'task-active-cancel', got %q", calls[0].TaskMessage.ID)
	}

	// Verify: taskboard has cancel_note set but task completed normally
	board := loadTaskboard(t, poolDir)
	task, ok := board.Tasks["task-active-cancel"]
	if !ok {
		t.Fatal("task-active-cancel not found in taskboard")
	}
	if task.CancelNote != "cancel requested while active" {
		t.Errorf("CancelNote = %q, want %q", task.CancelNote, "cancel requested while active")
	}
	// Task should be completed since the spawner succeeded
	if task.Status != "completed" {
		t.Errorf("Status = %q, want %q", task.Status, "completed")
	}
}

func TestDaemon_BlockedTaskNotSpawned(t *testing.T) {
	poolDir := t.TempDir()

	poolToml := `[pool]
name = "test-pool"
project_dir = "` + poolDir + `"

[experts.auth]
`
	os.WriteFile(filepath.Join(poolDir, "pool.toml"), []byte(poolToml), 0o644)

	cfg, _ := config.LoadPool(poolDir)
	fake := &fakeSpawner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := daemon.New(cfg, poolDir, logger, daemon.WithSpawner(fake))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	time.Sleep(500 * time.Millisecond)

	// Write a task with a dependency to postoffice
	msgContent := `---
id: task-blocked
from: architect
to: auth
type: task
depends-on: [task-prereq]
timestamp: 2026-04-01T14:32:00Z
---

This depends on task-prereq.
`
	postPath := filepath.Join(poolDir, "postoffice", "task-blocked.md")
	os.WriteFile(postPath, []byte(msgContent), 0o644)

	// Wait for routing + attempted processing
	time.Sleep(2 * time.Second)

	// Expert should NOT have been spawned (task is blocked)
	calls := fake.getCalls()
	if len(calls) != 0 {
		t.Errorf("expected 0 spawn calls for blocked task, got %d", len(calls))
	}

	// Verify taskboard shows blocked
	board := loadTaskboard(t, poolDir)
	task, ok := board.Tasks["task-blocked"]
	if !ok {
		t.Fatal("task-blocked not found in taskboard")
	}
	if task.Status != "blocked" {
		t.Errorf("Status = %q, want %q", task.Status, "blocked")
	}

	shutdownDaemon(t, cancel, errCh)
}
