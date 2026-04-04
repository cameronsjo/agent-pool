// Test plan for internal/daemon:
//
// Lifecycle:
//   - EnsureDirsAndRouting: dirs created, message routed, postoffice cleaned
//   - HandleInboxWithFakeSpawner: spawn called, log written, inbox cleaned
//   - ExpertBusyQueuesMessage: second message queued while first blocks, both processed
//   - ResolveExpertConfig: expert-level model overrides pool default
//   - NonZeroExitPreservesInbox: log written, inbox preserved on failure
//   - DrainPostoffice: pre-existing message routed on startup
//   - SessionTimeoutCancelsSpawn: spawn cancelled, inbox preserved, no log
//
// Taskboard:
//   - TaskboardCreatedOnStart: taskboard.json written with routed task
//   - TaskStatusLifecycle: pending → active → completed, timestamps/exit/meta fields set
//   - TaskFailedTrackedInTaskboard: status=failed, exit_code=1
//   - BlockedTaskNotSpawned: depends-on blocks spawn, status=blocked
//   - CycleRejectedOnRegister: mutual depends-on rejects second task, neither spawned
//   - DuplicateTaskIDIgnored: second task with same ID not added, expert spawned once
//
// Cancel:
//   - CancelPendingTask: blocked task cancelled, inbox removed, expert not spawned
//   - CancelActiveTask: cancel_note set, task completes normally
//   - CancelCompletedTaskIsNoop: completed task unchanged by cancel
//   - CancelMissingCancelsField: no crash, no spawn, taskboard empty
//
// Handoff:
//   - HandoffIncrementsCount: handoff_count=1, needs_attention=false
//   - HandoffEscalation: handoff_count=2, needs_attention=true
//
// Architect:
//   - ArchitectSpawn: message to architect routes and spawns with opus model
//   - ArchitectConfigResolution: architect uses opus, auth uses haiku
//   - ArchitectInboxDrainOnStart: pre-existing inbox message processed on startup
//   - NotifyRoutedNotRegistered: notify messages route to inbox but skip taskboard
package daemon_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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
	mu       sync.Mutex
	calls    []*expert.SpawnConfig
	attempts int // incremented on every Spawn call, including cancelled ones

	// result and err are returned from Spawn. Defaults to a zero-value Result if nil.
	result *expert.Result
	err    error

	// If set, Spawn blocks on this channel before returning.
	gate chan struct{}
}

func (f *fakeSpawner) Spawn(ctx context.Context, _ *slog.Logger, cfg *expert.SpawnConfig) (*expert.Result, error) {
	f.mu.Lock()
	f.attempts++
	f.mu.Unlock()

	if f.gate != nil {
		select {
		case <-f.gate:
		case <-ctx.Done():
			return &expert.Result{
				TaskID:   cfg.TaskMessage.ID,
				ExitCode: -1,
				Duration: 0,
			}, nil
		}
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

func (f *fakeSpawner) getAttempts() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts
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
	if task.Expert != "auth" {
		t.Errorf("Expert = %q, want %q", task.Expert, "auth")
	}
	if task.From != "architect" {
		t.Errorf("From = %q, want %q", task.From, "architect")
	}
	if task.Type != "task" {
		t.Errorf("Type = %q, want %q", task.Type, "task")
	}
	if task.Priority != "normal" {
		t.Errorf("Priority = %q, want %q", task.Priority, "normal")
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

func TestDaemon_SessionTimeoutCancelsSpawn(t *testing.T) {
	poolDir := t.TempDir()

	// Configure pool with 1s session timeout
	poolToml := `[pool]
name = "test-pool"
project_dir = "` + poolDir + `"

[defaults]
model = "sonnet"
session_timeout = "1s"

[experts.auth]
`
	os.WriteFile(filepath.Join(poolDir, "pool.toml"), []byte(poolToml), 0o644)

	cfg, err := config.LoadPool(poolDir)
	if err != nil {
		t.Fatalf("LoadPool: %v", err)
	}

	// Gate that never closes — spawn blocks until context cancels
	gate := make(chan struct{})
	defer close(gate)
	fake := &fakeSpawner{gate: gate}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := daemon.New(cfg, poolDir, logger, daemon.WithSpawner(fake))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	time.Sleep(500 * time.Millisecond)

	// Write task to auth's inbox
	inboxDir := filepath.Join(poolDir, "experts", "auth", "inbox")
	writeMessage(t, inboxDir, "task-timeout-001", "architect", "auth")

	// Poll until the spawn was attempted and the 1s timeout fires
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fake.getAttempts() > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if fake.getAttempts() == 0 {
		t.Fatal("expected spawn to be attempted")
	}

	// Wait for the 1s timeout to expire and the daemon to process the failure
	time.Sleep(2 * time.Second)

	// Inbox file should be preserved (non-zero exit code from timeout)
	inboxFile := filepath.Join(inboxDir, "task-timeout-001.md")
	if _, err := os.Stat(inboxFile); os.IsNotExist(err) {
		t.Error("inbox file should be preserved when spawn is cancelled by timeout")
	}

	// Taskboard should show the task as failed
	board := loadTaskboard(t, poolDir)
	task, ok := board.Tasks["task-timeout-001"]
	if !ok {
		t.Fatal("task-timeout-001 not found in taskboard")
	}
	if task.Status != "failed" {
		t.Errorf("Status = %q, want %q", task.Status, "failed")
	}

	shutdownDaemon(t, cancel, errCh)
}

// writeHandoff writes a handoff mail file to the given directory.
func writeHandoff(t *testing.T, dir, id, from, to string) string {
	t.Helper()
	content := fmt.Sprintf(`---
id: %s
from: %s
to: %s
type: handoff
timestamp: 2026-04-01T15:00:00Z
---

Context exhaustion, need fresh session.
`, id, from, to)
	path := filepath.Join(dir, id+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing handoff %s: %v", id, err)
	}
	return path
}

func TestDaemon_HandoffIncrementsCount(t *testing.T) {
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

	writeMessage(t, filepath.Join(poolDir, "postoffice"), "task-handoff-001", "architect", "auth")

	// Wait for the task to become active (blocked on gate)
	time.Sleep(1 * time.Second)

	writeHandoff(t, filepath.Join(poolDir, "postoffice"), "handoff-001", "auth", "architect")

	time.Sleep(1 * time.Second)

	board := loadTaskboard(t, poolDir)
	task, ok := board.Tasks["task-handoff-001"]
	if !ok {
		t.Fatal("task-handoff-001 not found in taskboard")
	}
	if task.HandoffCount != 1 {
		t.Errorf("HandoffCount = %d, want 1", task.HandoffCount)
	}
	if task.NeedsAttention {
		t.Error("NeedsAttention should be false after 1 handoff")
	}

	close(gate)
	shutdownDaemon(t, cancel, errCh)
}

func TestDaemon_HandoffEscalation(t *testing.T) {
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

	writeMessage(t, filepath.Join(poolDir, "postoffice"), "task-escalate-001", "architect", "auth")

	time.Sleep(1 * time.Second)

	writeHandoff(t, filepath.Join(poolDir, "postoffice"), "handoff-esc-001", "auth", "architect")
	time.Sleep(500 * time.Millisecond)
	writeHandoff(t, filepath.Join(poolDir, "postoffice"), "handoff-esc-002", "auth", "architect")

	time.Sleep(1 * time.Second)

	board := loadTaskboard(t, poolDir)
	task, ok := board.Tasks["task-escalate-001"]
	if !ok {
		t.Fatal("task-escalate-001 not found in taskboard")
	}
	if task.HandoffCount != 2 {
		t.Errorf("HandoffCount = %d, want 2", task.HandoffCount)
	}
	if !task.NeedsAttention {
		t.Error("NeedsAttention should be true after 2 handoffs")
	}

	close(gate)
	shutdownDaemon(t, cancel, errCh)
}

func TestDaemon_CycleRejectedOnRegister(t *testing.T) {
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

	postDir := filepath.Join(poolDir, "postoffice")

	// Route task-A that depends on task-B
	msgA := `---
id: task-A
from: architect
to: auth
type: task
depends-on: [task-B]
timestamp: 2026-04-01T14:32:00Z
---

Task A depends on B.
`
	os.WriteFile(filepath.Join(postDir, "task-A.md"), []byte(msgA), 0o644)

	// Wait for task-A to be routed and registered
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(poolDir, "taskboard.json")); err == nil {
			b := loadTaskboard(t, poolDir)
			if _, ok := b.Tasks["task-A"]; ok {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Route task-B that depends on task-A — creates a cycle
	msgB := `---
id: task-B
from: architect
to: auth
type: task
depends-on: [task-A]
timestamp: 2026-04-01T14:33:00Z
---

Task B depends on A (cycle).
`
	os.WriteFile(filepath.Join(postDir, "task-B.md"), []byte(msgB), 0o644)

	// Wait for task-B to be processed (postoffice file removed)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(postDir, "task-B.md")); os.IsNotExist(err) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Allow processing time
	time.Sleep(500 * time.Millisecond)

	// Verify: task-A is in the taskboard as blocked
	board := loadTaskboard(t, poolDir)
	taskA, okA := board.Tasks["task-A"]
	if !okA {
		t.Fatal("task-A not found in taskboard")
	}
	if taskA.Status != "blocked" {
		t.Errorf("task-A Status = %q, want %q", taskA.Status, "blocked")
	}

	// task-B is rejected by both registerTask (cycle detected via ValidateAdd)
	// and ensureTaskRegistered (same validation path). It should NOT be in
	// the taskboard — cycle-creating tasks are fully rejected.
	if _, okB := board.Tasks["task-B"]; okB {
		t.Error("task-B should not be in taskboard (cycle rejected)")
	}

	// Verify: expert was never spawned for either task
	calls := fake.getCalls()
	for _, c := range calls {
		if c.TaskMessage.ID == "task-A" || c.TaskMessage.ID == "task-B" {
			t.Errorf("expert should not have been spawned for %s", c.TaskMessage.ID)
		}
	}

	shutdownDaemon(t, cancel, errCh)
}

func TestDaemon_CancelCompletedTaskIsNoop(t *testing.T) {
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

	// Route a task and let it complete
	writeMessage(t, postDir, "task-done-001", "architect", "auth")

	// Poll for spawn completion
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(fake.getCalls()) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Allow post-spawn processing
	time.Sleep(500 * time.Millisecond)

	// Confirm task is completed
	board := loadTaskboard(t, poolDir)
	task, ok := board.Tasks["task-done-001"]
	if !ok {
		t.Fatal("task-done-001 not found in taskboard")
	}
	if task.Status != "completed" {
		t.Fatalf("Status = %q, want %q (task must be completed before cancel test)", task.Status, "completed")
	}

	// Send a cancel targeting the completed task
	cancelPath := writeCancelMessage(t, postDir, "cancel-done-001", "task-done-001", "architect", "auth")

	// Wait for cancel file to be consumed from postoffice
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(cancelPath); os.IsNotExist(err) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	time.Sleep(500 * time.Millisecond)

	// Verify: task is still completed, no cancel_note, no status change
	board = loadTaskboard(t, poolDir)
	task, ok = board.Tasks["task-done-001"]
	if !ok {
		t.Fatal("task-done-001 disappeared from taskboard after cancel")
	}
	if task.Status != "completed" {
		t.Errorf("Status = %q, want %q (should remain completed)", task.Status, "completed")
	}
	if task.CancelNote != "" {
		t.Errorf("CancelNote = %q, want empty (cancel of completed task is a no-op)", task.CancelNote)
	}

	shutdownDaemon(t, cancel, errCh)
}

func TestDaemon_CancelMissingCancelsField(t *testing.T) {
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

	postDir := filepath.Join(poolDir, "postoffice")

	// Write a cancel message with no cancels field
	cancelContent := `---
id: cancel-empty
from: architect
to: auth
type: cancel
timestamp: 2026-04-01T14:32:00Z
---

Cancel without target.
`
	cancelPath := filepath.Join(postDir, "cancel-empty.md")
	os.WriteFile(cancelPath, []byte(cancelContent), 0o644)

	// Wait for cancel file to be consumed from postoffice
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(cancelPath); os.IsNotExist(err) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if _, err := os.Stat(cancelPath); !os.IsNotExist(err) {
		t.Error("cancel file should have been removed from postoffice")
	}

	// Allow any processing
	time.Sleep(500 * time.Millisecond)

	// Verify: no crashes (daemon still running), expert never spawned, taskboard empty
	if len(fake.getCalls()) != 0 {
		t.Errorf("expected 0 spawn calls, got %d", len(fake.getCalls()))
	}

	// Taskboard should either not exist or be empty
	boardPath := filepath.Join(poolDir, "taskboard.json")
	if _, err := os.Stat(boardPath); err == nil {
		board := loadTaskboard(t, poolDir)
		if len(board.Tasks) != 0 {
			t.Errorf("expected empty taskboard, got %d tasks", len(board.Tasks))
		}
	}

	shutdownDaemon(t, cancel, errCh)
}

func TestDaemon_DuplicateTaskIDIgnored(t *testing.T) {
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

	// Route first task
	writeMessage(t, postDir, "task-dup-001", "architect", "auth")

	// Wait for it to be registered in taskboard
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(poolDir, "taskboard.json")); err == nil {
			b := loadTaskboard(t, poolDir)
			if _, ok := b.Tasks["task-dup-001"]; ok {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Wait for first spawn to complete
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(fake.getCalls()) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Allow post-spawn processing
	time.Sleep(500 * time.Millisecond)

	// Route the same task ID again
	writeMessage(t, postDir, "task-dup-001", "architect", "auth")

	// Wait for the duplicate to be processed (postoffice file removed)
	dupPath := filepath.Join(postDir, "task-dup-001.md")
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(dupPath); os.IsNotExist(err) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Allow processing
	time.Sleep(500 * time.Millisecond)

	// Verify: taskboard still has exactly one entry
	board := loadTaskboard(t, poolDir)
	if _, ok := board.Tasks["task-dup-001"]; !ok {
		t.Fatal("task-dup-001 should still be in taskboard")
	}

	// Verify: expert spawned exactly once
	calls := fake.getCalls()
	spawnCount := 0
	for _, c := range calls {
		if c.TaskMessage.ID == "task-dup-001" {
			spawnCount++
		}
	}
	if spawnCount != 1 {
		t.Errorf("expected 1 spawn for task-dup-001, got %d", spawnCount)
	}

	shutdownDaemon(t, cancel, errCh)
}

// --- Architect spawning tests ---

func TestDaemon_ArchitectSpawn(t *testing.T) {
	poolDir := t.TempDir()

	poolToml := `[pool]
name = "test-pool"
project_dir = "` + poolDir + `"

[architect]
model = "opus"

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

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	time.Sleep(500 * time.Millisecond)

	// Route a message to architect via postoffice
	writeMessage(t, filepath.Join(poolDir, "postoffice"), "task-arch-001", "concierge", "architect")

	// Wait for spawn
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(fake.getCalls()) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	calls := fake.getCalls()
	if len(calls) == 0 {
		t.Fatal("expected architect spawn, got none")
	}

	call := calls[0]
	if call.Name != "architect" {
		t.Errorf("spawn name = %q, want architect", call.Name)
	}
	if call.Model != "opus" {
		t.Errorf("spawn model = %q, want opus", call.Model)
	}
	if call.TaskMessage.ID != "task-arch-001" {
		t.Errorf("task ID = %q, want task-arch-001", call.TaskMessage.ID)
	}

	// Verify log file written to architect dir (not experts/architect)
	logPath := filepath.Join(poolDir, "architect", "logs", "task-arch-001.json")
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(logPath); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("log should be written to architect/logs/, not experts/architect/logs/")
	}

	shutdownDaemon(t, cancel, errCh)
}

func TestDaemon_ArchitectConfigResolution(t *testing.T) {
	poolDir := t.TempDir()

	poolToml := `[pool]
name = "test-pool"
project_dir = "` + poolDir + `"

[defaults]
model = "sonnet"

[architect]
model = "opus"
session_timeout = "30m"

[experts.auth]
model = "haiku"
`
	os.WriteFile(filepath.Join(poolDir, "pool.toml"), []byte(poolToml), 0o644)

	cfg, err := config.LoadPool(poolDir)
	if err != nil {
		t.Fatalf("LoadPool: %v", err)
	}

	fake := &fakeSpawner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := daemon.New(cfg, poolDir, logger, daemon.WithSpawner(fake))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	time.Sleep(500 * time.Millisecond)

	// Route to architect
	writeMessage(t, filepath.Join(poolDir, "postoffice"), "task-cfg-arch", "concierge", "architect")
	// Route to auth
	writeMessage(t, filepath.Join(poolDir, "postoffice"), "task-cfg-auth", "architect", "auth")

	// Wait for both spawns
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(fake.getCalls()) >= 2 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	calls := fake.getCalls()
	if len(calls) < 2 {
		t.Fatalf("expected 2 spawns, got %d", len(calls))
	}

	// Find each call
	for _, c := range calls {
		switch c.Name {
		case "architect":
			if c.Model != "opus" {
				t.Errorf("architect model = %q, want opus", c.Model)
			}
		case "auth":
			if c.Model != "haiku" {
				t.Errorf("auth model = %q, want haiku", c.Model)
			}
		}
	}

	shutdownDaemon(t, cancel, errCh)
}

func TestDaemon_ArchitectInboxDrainOnStart(t *testing.T) {
	poolDir := t.TempDir()

	poolToml := `[pool]
name = "test-pool"
project_dir = "` + poolDir + `"

[architect]
model = "opus"

[experts.auth]
`
	os.WriteFile(filepath.Join(poolDir, "pool.toml"), []byte(poolToml), 0o644)

	cfg, err := config.LoadPool(poolDir)
	if err != nil {
		t.Fatalf("LoadPool: %v", err)
	}

	// Pre-create inbox and put a message there before daemon starts
	architectInbox := filepath.Join(poolDir, "architect", "inbox")
	os.MkdirAll(architectInbox, 0o755)
	os.MkdirAll(filepath.Join(poolDir, "architect", "logs"), 0o755)
	os.MkdirAll(filepath.Join(poolDir, "postoffice"), 0o755)
	os.MkdirAll(filepath.Join(poolDir, "experts", "auth", "inbox"), 0o755)
	os.MkdirAll(filepath.Join(poolDir, "experts", "auth", "logs"), 0o755)

	writeMessage(t, architectInbox, "task-predrain-001", "concierge", "architect")

	fake := &fakeSpawner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := daemon.New(cfg, poolDir, logger, daemon.WithSpawner(fake))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	// Wait for drain to process pre-existing message
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(fake.getCalls()) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	calls := fake.getCalls()
	if len(calls) == 0 {
		t.Fatal("expected pre-existing architect inbox message to be processed on startup")
	}
	if calls[0].Name != "architect" {
		t.Errorf("spawn name = %q, want architect", calls[0].Name)
	}

	shutdownDaemon(t, cancel, errCh)
}

func TestDaemon_NotifyRoutedNotRegistered(t *testing.T) {
	poolDir := t.TempDir()

	poolToml := `[pool]
name = "test-pool"
project_dir = "` + poolDir + `"

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

	time.Sleep(500 * time.Millisecond)

	// Write a notify message (contract amendment notification) to postoffice
	notifyContent := fmt.Sprintf(`---
id: notify-contract-001-v2-auth
from: architect
to: auth
type: notify
contracts: [contract-001]
timestamp: 2026-04-01T14:32:00Z
---

Contract contract-001 has been amended to version 2.
`)
	postPath := filepath.Join(poolDir, "postoffice", "notify-contract-001-v2-auth.md")
	os.WriteFile(postPath, []byte(notifyContent), 0o644)

	// Wait for routing
	inboxFile := filepath.Join(poolDir, "experts", "auth", "inbox", "notify-contract-001-v2-auth.md")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(inboxFile); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if _, err := os.Stat(inboxFile); os.IsNotExist(err) {
		t.Fatal("notify message was not routed to auth inbox")
	}

	// Allow processing time
	time.Sleep(500 * time.Millisecond)

	// Verify: taskboard does NOT contain this notify message
	boardPath := filepath.Join(poolDir, "taskboard.json")
	if _, err := os.Stat(boardPath); err == nil {
		board := loadTaskboard(t, poolDir)
		if _, ok := board.Tasks["notify-contract-001-v2-auth"]; ok {
			t.Error("notify message should NOT be registered in taskboard")
		}
	}

	shutdownDaemon(t, cancel, errCh)
}

func TestDaemon_ApprovalStdoutMode(t *testing.T) {
	poolDir := t.TempDir()

	poolToml := `[pool]
name = "test-pool"
project_dir = "` + poolDir + `"

[architect]
model = "opus"
approval_mode = "decomposition"
human_inbox = "stdout"

[experts.auth]
`
	os.WriteFile(filepath.Join(poolDir, "pool.toml"), []byte(poolToml), 0o644)

	cfg, err := config.LoadPool(poolDir)
	if err != nil {
		t.Fatalf("LoadPool: %v", err)
	}

	// Pipe "y\n" to stdin to auto-approve
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer stdinReader.Close()
	defer stdinWriter.Close()
	var stdoutBuf bytes.Buffer

	fake := &fakeSpawner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := daemon.New(cfg, poolDir, logger,
		daemon.WithSpawner(fake),
		daemon.WithStdin(stdinReader),
		daemon.WithStdout(&stdoutBuf),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	time.Sleep(500 * time.Millisecond)

	// Write a pending approval request to approvals/
	approvalsDir := filepath.Join(poolDir, "approvals")
	proposalContent := "Task: task-approval-test\nTo: auth\n\nImplement token endpoint"
	os.WriteFile(filepath.Join(approvalsDir, "task-approval-test.proposal.md"), []byte(proposalContent), 0o644)

	// Wait for daemon to detect and present the proposal
	time.Sleep(500 * time.Millisecond)

	// Write approval via stdin
	stdinWriter.Write([]byte("y\n"))

	// Wait for response file
	approvedPath := filepath.Join(approvalsDir, "task-approval-test.approved")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(approvedPath); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if _, err := os.Stat(approvedPath); os.IsNotExist(err) {
		t.Error("expected .approved file after stdin approval")
	}

	// Verify stdout contains the proposal
	output := stdoutBuf.String()
	if !strings.Contains(output, "APPROVAL REQUEST") {
		t.Errorf("stdout should contain approval prompt, got %q", output)
	}

	shutdownDaemon(t, cancel, errCh)
}

func TestDaemon_ApprovalNoneAutoApproves(t *testing.T) {
	poolDir := t.TempDir()

	poolToml := `[pool]
name = "test-pool"
project_dir = "` + poolDir + `"

[architect]
model = "opus"
approval_mode = "none"

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

	time.Sleep(500 * time.Millisecond)

	// Write a pending approval
	approvalsDir := filepath.Join(poolDir, "approvals")
	os.WriteFile(filepath.Join(approvalsDir, "task-auto-001.proposal.md"), []byte("auto task"), 0o644)

	// Should auto-approve in "none" mode
	approvedPath := filepath.Join(approvalsDir, "task-auto-001.approved")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(approvedPath); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if _, err := os.Stat(approvedPath); os.IsNotExist(err) {
		t.Error("expected auto-approval in none mode")
	}

	shutdownDaemon(t, cancel, errCh)
}
