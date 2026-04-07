// Package daemon implements the core agent-pool process supervisor.
//
// The daemon watches pool directories for mail delivery via fsnotify,
// routes messages between agents, spawns Claude Code sessions for experts,
// and manages the task lifecycle.
package daemon

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cameronsjo/agent-pool/internal/approval"
	"github.com/cameronsjo/agent-pool/internal/config"
	"github.com/cameronsjo/agent-pool/internal/expert"
	"github.com/cameronsjo/agent-pool/internal/mail"
	agentmcp "github.com/cameronsjo/agent-pool/internal/mcp"
	"github.com/cameronsjo/agent-pool/internal/taskboard"
)

// Spawner abstracts expert session spawning for testability.
type Spawner interface {
	Spawn(ctx context.Context, logger *slog.Logger, cfg *expert.SpawnConfig) (*expert.Result, error)
}

// defaultSpawner delegates to expert.Spawn.
type defaultSpawner struct{}

func (defaultSpawner) Spawn(ctx context.Context, logger *slog.Logger, cfg *expert.SpawnConfig) (*expert.Result, error) {
	return expert.Spawn(ctx, logger, cfg)
}

// Daemon is the agent-pool process supervisor.
type Daemon struct {
	cfg     *config.PoolConfig
	poolDir string
	logger  *slog.Logger
	spawner Spawner
	stdin   io.Reader // for approval stdin (defaults to os.Stdin)
	stdout  io.Writer // for approval stdout (defaults to os.Stdout)

	mu        sync.Mutex
	board     *taskboard.Board
	boardPath string
	draining  map[string]bool // re-entrancy guard for expert inbox drains

	wg           sync.WaitGroup
	drainTimeout time.Duration // max wait for in-flight goroutines on shutdown (default 30s)
	startedAt    time.Time
	sockPathOver string // overrides default socket path (for tests with long TempDir paths)
	events       *eventBus
	sharedSet    map[string]bool // cached shared.include lookup set, built once in New
	curation     *curationScheduler
}

// Option configures a Daemon.
type Option func(*Daemon)

// WithSpawner sets a custom spawner (used in tests).
func WithSpawner(s Spawner) Option {
	return func(d *Daemon) { d.spawner = s }
}

// WithStdin sets a custom stdin reader for approval input (used in tests).
func WithStdin(r io.Reader) Option {
	return func(d *Daemon) { d.stdin = r }
}

// WithStdout sets a custom stdout writer for approval output (used in tests).
func WithStdout(w io.Writer) Option {
	return func(d *Daemon) { d.stdout = w }
}

// WithSocketPath overrides the default socket path ({poolDir}/daemon.sock).
// Used in tests where TempDir paths exceed the macOS Unix socket limit (104 bytes).
func WithSocketPath(path string) Option {
	return func(d *Daemon) { d.sockPathOver = path }
}

// WithDrainTimeout sets the max wait for in-flight goroutines during shutdown.
// Default is 30 seconds. Use a shorter value in tests.
func WithDrainTimeout(d time.Duration) Option {
	return func(dm *Daemon) { dm.drainTimeout = d }
}

// New creates a Daemon for the given pool.
func New(cfg *config.PoolConfig, poolDir string, logger *slog.Logger, opts ...Option) *Daemon {
	boardPath := filepath.Join(poolDir, "taskboard.json")
	board, err := taskboard.Load(boardPath)
	if err != nil {
		// Non-fatal: start with empty board. Log the error for diagnosis.
		logger.Warn("Failed to load taskboard, starting with empty board",
			"path", boardPath,
			"error", err,
		)
		board = taskboard.New()
	}

	d := &Daemon{
		cfg:          cfg,
		poolDir:      poolDir,
		logger:       logger,
		board:        board,
		boardPath:    boardPath,
		draining:     make(map[string]bool),
		spawner:      defaultSpawner{},
		drainTimeout: 30 * time.Second,
		events:       newEventBus(),
		curation:     newCurationScheduler(&cfg.Curation, poolDir, logger),
	}

	// Build cached shared expert lookup set
	if len(cfg.Shared.Include) > 0 {
		d.sharedSet = make(map[string]bool, len(cfg.Shared.Include))
		for _, name := range cfg.Shared.Include {
			d.sharedSet[name] = true
		}
	}

	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Run starts the daemon's main loop. It blocks until ctx is cancelled (via
// signal) or a stop command is received over the socket.
func (d *Daemon) Run(ctx context.Context) error {
	if err := d.ensureDirs(); err != nil {
		return fmt.Errorf("ensuring directory structure: %w", err)
	}

	// Create a child context so both signals and socket stop converge here.
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	watcher, err := NewWatcher(d.logger)
	if err != nil {
		return fmt.Errorf("creating watcher: %w", err)
	}
	defer watcher.Close()

	// Watch postoffice
	postofficeDir := filepath.Join(d.poolDir, "postoffice")
	if err := watcher.Add(postofficeDir); err != nil {
		return fmt.Errorf("watching postoffice: %w", err)
	}

	// Watch built-in role inboxes (architect, researcher)
	architectInbox := mail.ResolveInbox(d.poolDir, "architect")
	if err := watcher.Add(architectInbox); err != nil {
		return fmt.Errorf("watching architect inbox: %w", err)
	}
	researcherInbox := mail.ResolveInbox(d.poolDir, "researcher")
	if err := watcher.Add(researcherInbox); err != nil {
		return fmt.Errorf("watching researcher inbox: %w", err)
	}

	// Watch approvals directory for human approval requests
	approvalsDir := filepath.Join(d.poolDir, "approvals")
	if err := watcher.Add(approvalsDir); err != nil {
		return fmt.Errorf("watching approvals: %w", err)
	}

	// Watch each expert's inbox
	for name := range d.cfg.Experts {
		inboxDir := mail.ResolveInbox(d.poolDir, name)
		if err := watcher.Add(inboxDir); err != nil {
			return fmt.Errorf("watching inbox for %s: %w", name, err)
		}
	}

	// Watch shared expert inboxes (pool-scoped under shared-state/)
	for _, name := range d.cfg.Shared.Include {
		inboxDir := mail.ResolveSharedInbox(d.poolDir, name)
		if err := watcher.Add(inboxDir); err != nil {
			return fmt.Errorf("watching shared inbox for %s: %w", name, err)
		}
	}

	// Start socket server for CLI→daemon communication
	sockPath := d.resolveSockPath()
	sock, err := newSocketServer(sockPath, d, cancel)
	if err != nil {
		return fmt.Errorf("starting socket server: %w", err)
	}
	defer sock.close()
	go sock.serve(childCtx)

	// Start watcher goroutine
	go watcher.Run(childCtx)

	d.startedAt = time.Now()
	d.logger.Info("Successfully started daemon",
		"pool", d.cfg.Pool.Name,
		"pool_dir", d.poolDir,
		"experts", len(d.cfg.Experts),
		"socket", sockPath,
	)

	// Drain pre-existing messages from before the daemon started
	d.drainPostoffice(childCtx)
	d.drainAllInboxes(childCtx)

	// Start time-based curation ticker
	if d.curation.intervalHours > 0 {
		d.wg.Add(1)
		go func() {
			defer d.wg.Done()
			ticker := time.NewTicker(time.Duration(d.curation.intervalHours) * time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-childCtx.Done():
					return
				case <-ticker.C:
					if d.curation.ShouldTriggerByTime() {
						d.triggerCuration("time_interval")
					}
				}
			}
		}()
	}

	// Main event loop
	for {
		select {
		case <-childCtx.Done():
			d.logger.Info("Preparing to drain in-flight work",
				"pool", d.cfg.Pool.Name,
				"drain_timeout", d.drainTimeout,
			)
			done := make(chan struct{})
			go func() { d.wg.Wait(); close(done) }()
			select {
			case <-done:
				d.logger.Info("Successfully drained all in-flight work")
			case <-time.After(d.drainTimeout):
				d.logger.Warn("Skipping drain. Reason: timeout exceeded",
					"drain_timeout", d.drainTimeout,
				)
			}
			return nil

		case event, ok := <-watcher.Events():
			if !ok {
				return nil
			}

			if event.Dir == postofficeDir {
				d.handlePostoffice(childCtx, event.Path)
			} else if event.Dir == approvalsDir {
				d.wg.Add(1)
				go func() { defer d.wg.Done(); d.handleApprovalRequest(childCtx, event.Path) }()
			} else {
				// Determine which expert this inbox belongs to
				expertName := d.resolveExpertName(event.Dir)
				if expertName != "" {
					// Dispatch in a goroutine so expert spawns don't
					// block postoffice routing or other experts.
					// The busy flag inside handleInbox prevents
					// concurrent spawns for the same expert.
					d.wg.Add(1)
					go func() { defer d.wg.Done(); d.handleInbox(childCtx, expertName, event.Path) }()
				} else {
					d.logger.Warn("Received event for unknown inbox",
						"dir", event.Dir,
						"path", event.Path,
					)
				}
			}
		}
	}
}

// handlePostoffice routes a message from the postoffice to the recipient's inbox
// and registers task-type messages in the taskboard. Cancel messages are
// intercepted and consumed by the daemon — they are never routed to inboxes.
func (d *Daemon) handlePostoffice(ctx context.Context, path string) {
	// Parse first to check for cancel messages before routing.
	msg, err := mail.ParseFile(path)
	if err != nil {
		d.logger.Error("Failed to parse postoffice message",
			"path", path,
			"error", err,
		)
		return
	}

	if msg.Type == mail.TypeCancel {
		d.handleCancel(msg, path)
		return
	}

	// Non-cancel: route to recipient inbox.
	routed, err := mail.Route(d.logger, d.poolDir, path, d.sharedNamesMap())
	if err != nil {
		d.logger.Error("Failed to route message",
			"path", path,
			"error", err,
		)
		return
	}

	d.logger.Info("Successfully routed message",
		"id", routed.ID,
		"to", routed.To,
	)
	d.events.emit(Event{
		Type:      EventTaskRouted,
		Timestamp: time.Now(),
		Data:      TaskRoutedData{ID: routed.ID, From: routed.From, To: routed.To, Type: string(routed.Type)},
	})

	if routed.Type == mail.TypeTask || routed.Type == mail.TypeQuestion {
		d.registerTask(routed)
	}

	if routed.Type == mail.TypeHandoff {
		d.handleHandoff(routed)
	}
}

// handleHandoff records a handoff event against the active task for the expert
// that sent the message. Escalates to needs_attention after repeated handoffs.
func (d *Daemon) handleHandoff(msg *mail.Message) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var activeTaskID string
	for _, t := range d.board.Tasks {
		if t.Expert == msg.From && t.Status == taskboard.StatusActive {
			activeTaskID = t.ID
			break
		}
	}

	if activeTaskID == "" {
		d.logger.Warn("Received handoff without active task",
			"from", msg.From,
		)
		return
	}

	if err := d.board.RecordHandoff(activeTaskID); err != nil {
		d.logger.Error("Failed to record handoff",
			"task_id", activeTaskID,
			"error", err,
		)
		return
	}

	task, _ := d.board.Get(activeTaskID)
	if task.NeedsAttention {
		d.logger.Warn("Task escalated after multiple handoffs",
			"task_id", activeTaskID,
			"expert", msg.From,
			"handoff_count", task.HandoffCount,
		)
	} else {
		d.logger.Info("Successfully recorded handoff",
			"task_id", activeTaskID,
			"expert", msg.From,
			"handoff_count", task.HandoffCount,
		)
	}

	d.board.Save(d.boardPath)
}

// handleCancel processes a cancel message: updates the taskboard, removes any
// pending inbox file, and deletes the cancel message from the postoffice.
func (d *Daemon) handleCancel(msg *mail.Message, cancelPath string) {
	targetID := msg.Cancels
	if targetID == "" {
		d.logger.Warn("Skipping cancel message. Reason: missing cancels field",
			"id", msg.ID,
		)
		os.Remove(cancelPath)
		return
	}

	d.mu.Lock()
	task, ok := d.board.Get(targetID)
	if !ok {
		d.mu.Unlock()
		d.logger.Info("Skipping cancel. Reason: target task not found in taskboard",
			"cancel_id", msg.ID,
			"target_id", targetID,
		)
		os.Remove(cancelPath)
		return
	}

	switch task.Status {
	case taskboard.StatusPending, taskboard.StatusBlocked:
		task.Status = taskboard.StatusCancelled
		now := time.Now().UTC()
		task.CompletedAt = &now

		d.board.EvaluateDeps()
		d.board.Save(d.boardPath)
		d.mu.Unlock()

		// Remove the inbox file for this task if it exists.
		var cancelInboxDir string
		if d.isSharedExpert(task.Expert) {
			cancelInboxDir = mail.ResolveSharedInbox(d.poolDir, task.Expert)
		} else {
			cancelInboxDir = mail.ResolveInbox(d.poolDir, task.Expert)
		}
		inboxPath := filepath.Join(cancelInboxDir, targetID+".md")
		if err := os.Remove(inboxPath); err != nil && !os.IsNotExist(err) {
			d.logger.Warn("Failed to remove inbox file for cancelled task",
				"task_id", targetID,
				"path", inboxPath,
				"error", err,
			)
		}

		d.logger.Info("Successfully cancelled task",
			"cancel_id", msg.ID,
			"target_id", targetID,
		)
		d.events.emit(Event{
			Type:      EventTaskCancelled,
			Timestamp: time.Now(),
			Data:      TaskCancelledData{TaskID: targetID},
		})

	case taskboard.StatusActive:
		task.CancelNote = "cancel requested while active"
		d.board.Save(d.boardPath)
		d.mu.Unlock()

		d.logger.Warn("Cancel requested for active task, noting for post-completion review",
			"cancel_id", msg.ID,
			"target_id", targetID,
		)

	default:
		// Already completed, failed, or cancelled — no-op.
		d.mu.Unlock()
		d.logger.Info("Skipping cancel. Reason: task already terminal",
			"cancel_id", msg.ID,
			"target_id", targetID,
			"status", task.Status,
		)
	}

	os.Remove(cancelPath)
}

// registerTask adds a task-type message to the taskboard.
func (d *Daemon) registerTask(msg *mail.Message) {
	status := taskboard.StatusPending
	if len(msg.DependsOn) > 0 {
		status = taskboard.StatusBlocked
	}

	task := &taskboard.Task{
		ID:        msg.ID,
		Status:    status,
		Expert:    msg.To,
		DependsOn: msg.DependsOn,
		From:      msg.From,
		Type:      string(msg.Type),
		Priority:  string(msg.Priority),
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.board.ValidateAdd(task); err != nil {
		d.logger.Error("Failed to register task in taskboard",
			"task_id", msg.ID,
			"error", err,
		)
		return
	}

	if err := d.board.Add(task); err != nil {
		d.logger.Error("Failed to add task to taskboard",
			"task_id", msg.ID,
			"error", err,
		)
		return
	}

	// Recompute: dependencies may already be completed if prerequisites
	// arrived and finished before this task was registered.
	d.board.EvaluateDeps()

	if err := d.board.Save(d.boardPath); err != nil {
		d.logger.Error("Failed to save taskboard",
			"error", err,
		)
	}
}

// handleInbox is the entry point from the watcher event loop. It acquires the
// draining flag for the expert (re-entrancy guard), drains all queued inbox
// messages iteratively, then releases the flag.
func (d *Daemon) handleInbox(ctx context.Context, expertName string, _ string) {
	d.mu.Lock()
	if d.draining[expertName] {
		d.mu.Unlock()
		d.logger.Info("Skipping expert dispatch. Reason: expert busy",
			"expert", expertName,
		)
		return
	}
	d.draining[expertName] = true
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		d.draining[expertName] = false
		d.mu.Unlock()
	}()

	d.drainInbox(ctx, expertName)
}

// processInboxMessage handles a single inbox file: parse, check taskboard,
// spawn, log, update taskboard, and conditionally remove. Returns true if the
// file was successfully processed (regardless of exit code), false if parsing
// or spawning failed.
func (d *Daemon) processInboxMessage(ctx context.Context, expertName string, path string) bool {
	msg, err := mail.ParseFile(path)
	if err != nil {
		d.logger.Error("Failed to parse inbox message",
			"expert", expertName,
			"path", path,
			"error", err,
		)
		return false
	}

	// Ensure task is registered in the taskboard. Pre-existing inbox files
	// (from before the daemon started) may not be tracked yet.
	if err := d.ensureTaskRegistered(msg); err != nil {
		d.logger.Error("Failed to register pre-existing task",
			"expert", expertName,
			"task_id", msg.ID,
			"error", err,
		)
		return false
	}

	// Check taskboard status under lock — skip blocked or cancelled tasks.
	d.mu.Lock()
	task, tracked := d.board.Get(msg.ID)
	if tracked {
		switch task.Status {
		case taskboard.StatusBlocked:
			d.mu.Unlock()
			d.logger.Info("Skipping task. Reason: blocked on dependencies",
				"expert", expertName,
				"task_id", msg.ID,
			)
			return false
		case taskboard.StatusCancelled:
			d.mu.Unlock()
			d.logger.Info("Skipping task. Reason: task was cancelled",
				"expert", expertName,
				"task_id", msg.ID,
			)
			os.Remove(path)
			return true
		case taskboard.StatusCompleted, taskboard.StatusFailed:
			d.mu.Unlock()
			d.logger.Info("Skipping task. Reason: task already reached terminal status",
				"expert", expertName,
				"task_id", msg.ID,
				"status", task.Status,
			)
			os.Remove(path)
			return true
		}

		// Mark active
		now := time.Now().UTC()
		task.Status = taskboard.StatusActive
		task.StartedAt = &now
		d.board.Save(d.boardPath)

		d.logger.Info("Preparing to run task",
			"expert", expertName,
			"task_id", msg.ID,
		)
	}
	d.mu.Unlock()

	model, tools := d.resolveExpertConfig(expertName)

	d.events.emit(Event{
		Type:      EventExpertSpawning,
		Timestamp: time.Now(),
		Data:      ExpertSpawningData{Expert: expertName, TaskID: msg.ID, Model: model},
	})
	projectDir := d.resolveProjectDir()

	// Resolve directories: shared experts use user-level identity + pool overlay
	var expertDir, overlayDir, logDir string
	if d.isSharedExpert(expertName) {
		userDir, resolveErr := mail.ResolveSharedExpertDir(expertName)
		if resolveErr != nil {
			d.logger.Error("Failed to resolve shared expert directory",
				"expert", expertName,
				"task_id", msg.ID,
				"error", resolveErr,
			)
			d.markTaskFailed(msg.ID, -1)
			return false
		}
		expertDir = userDir
		overlayDir = filepath.Join(d.poolDir, "shared-state", expertName)
		logDir = overlayDir // WriteLog/AppendIndex create logs/ subdir inside this
	} else {
		expertDir = d.resolveExpertDir(expertName)
		logDir = expertDir
	}

	var mcpConfigPath string
	var mcpErr error
	if mail.IsBuiltinRole(expertName) {
		mcpConfigPath, mcpErr = agentmcp.WriteTempConfigForRole(d.poolDir, expertName)
	} else if d.isSharedExpert(expertName) {
		mcpConfigPath, mcpErr = agentmcp.WriteTempConfigShared(d.poolDir, expertName)
	} else {
		mcpConfigPath, mcpErr = agentmcp.WriteTempConfig(d.poolDir, expertName)
	}
	if mcpErr != nil {
		d.logger.Error("Failed to write MCP config",
			"expert", expertName,
			"task_id", msg.ID,
			"error", mcpErr,
		)
		d.markTaskFailed(msg.ID, -1)
		return false
	}
	defer func() {
		if err := os.Remove(mcpConfigPath); err != nil && !os.IsNotExist(err) {
			d.logger.Warn("Failed to remove MCP config temp file",
				"path", mcpConfigPath,
				"error", err,
			)
		}
	}()

	// Append pool MCP tool names so they're pre-approved in headless mode.
	// Built-in roles get their role-specific tools in addition to expert tools.
	allTools := append(tools, agentmcp.ToolNamesForRole(expertName)...)

	cfg := &expert.SpawnConfig{
		Name:          expertName,
		Model:         model,
		AllowedTools:  allTools,
		ProjectDir:    projectDir,
		ExpertDir:     expertDir,
		OverlayDir:    overlayDir,
		PoolDir:       d.poolDir,
		TaskMessage:   msg,
		MCPConfigPath: mcpConfigPath,
	}

	timeout, parseErr := d.resolveSessionTimeout(expertName)
	if parseErr != nil {
		d.logger.Warn("Failed to parse session timeout, running without timeout",
			"error", parseErr,
		)
	}

	var spawnCtx context.Context
	var spawnCancel context.CancelFunc
	if timeout > 0 {
		spawnCtx, spawnCancel = context.WithTimeout(ctx, timeout)
	} else {
		spawnCtx, spawnCancel = context.WithCancel(ctx)
	}
	defer spawnCancel()

	result, err := d.spawner.Spawn(spawnCtx, d.logger, cfg)
	if err != nil {
		d.logger.Error("Failed to spawn expert",
			"expert", expertName,
			"task_id", msg.ID,
			"error", err,
		)
		d.markTaskFailed(msg.ID, -1)
		return false
	}

	// Always write logs — the archive is append-only by design.
	// For shared experts, logs go to pool-scoped shared-state dir.
	if err := expert.WriteLog(logDir, result.TaskID, result.Output); err != nil {
		d.logger.Error("Failed to write task log",
			"expert", expertName,
			"task_id", result.TaskID,
			"error", err,
		)
	}

	if len(result.Stderr) > 0 {
		if err := expert.WriteStderr(logDir, result.TaskID, result.Stderr); err != nil {
			d.logger.Error("Failed to write stderr log",
				"expert", expertName,
				"task_id", result.TaskID,
				"error", err,
			)
		}
	}

	if err := expert.AppendIndex(logDir, &expert.LogEntry{
		TaskID:    result.TaskID,
		Timestamp: msg.Timestamp,
		From:      msg.From,
		ExitCode:  result.ExitCode,
		Summary:   result.Summary,
	}); err != nil {
		d.logger.Error("Failed to append log index",
			"expert", expertName,
			"task_id", result.TaskID,
			"error", err,
		)
	}

	if result.ExitCode != 0 {
		d.logger.Warn("Expert session failed",
			"expert", expertName,
			"task_id", result.TaskID,
			"exit_code", result.ExitCode,
			"duration", result.Duration,
			"summary", result.Summary,
		)
		d.events.emit(Event{
			Type:      EventExpertFailed,
			Timestamp: time.Now(),
			Data:      ExpertFailedData{Expert: expertName, TaskID: result.TaskID, ExitCode: result.ExitCode},
		})
		d.markTaskFailed(msg.ID, result.ExitCode)
		return true
	}

	// Remove inbox file only on success
	if err := os.Remove(path); err != nil {
		d.logger.Warn("Failed to remove processed inbox file",
			"path", path,
			"error", err,
		)
	}

	d.markTaskCompleted(ctx, msg.ID, result.ExitCode)

	d.logger.Info("Successfully completed task",
		"expert", expertName,
		"task_id", result.TaskID,
		"exit_code", result.ExitCode,
		"duration", result.Duration,
		"summary", result.Summary,
	)
	d.events.emit(Event{
		Type:      EventExpertCompleted,
		Timestamp: time.Now(),
		Data: ExpertCompletedData{
			Expert:   expertName,
			TaskID:   result.TaskID,
			Duration: result.Duration.String(),
			ExitCode: result.ExitCode,
			Summary:  result.Summary,
		},
	})

	return true
}

// ensureTaskRegistered adds a task to the taskboard if it isn't already tracked.
// This handles pre-existing inbox files from before the daemon started. Uses
// the same ValidateAdd path as registerTask to enforce cycle/duplicate checks.
// Only task and question types are tracked — notify and other types are skipped.
func (d *Daemon) ensureTaskRegistered(msg *mail.Message) error {
	// Only track task-like messages, matching the filter in handlePostoffice
	if msg.Type != mail.TypeTask && msg.Type != mail.TypeQuestion {
		return nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.board.Get(msg.ID); ok {
		return nil
	}

	status := taskboard.StatusPending
	if len(msg.DependsOn) > 0 {
		status = taskboard.StatusBlocked
	}

	task := &taskboard.Task{
		ID:        msg.ID,
		Status:    status,
		Expert:    msg.To,
		DependsOn: msg.DependsOn,
		From:      msg.From,
		Type:      string(msg.Type),
		Priority:  string(msg.Priority),
	}

	if err := d.board.ValidateAdd(task); err != nil {
		return fmt.Errorf("validating task %q: %w", msg.ID, err)
	}
	if err := d.board.Add(task); err != nil {
		return fmt.Errorf("adding task %q: %w", msg.ID, err)
	}
	d.board.EvaluateDeps()
	if err := d.board.Save(d.boardPath); err != nil {
		return fmt.Errorf("saving taskboard: %w", err)
	}
	return nil
}

// markTaskCompleted updates a task's status to completed, evaluates dependencies,
// wakes experts for newly-ready tasks, and saves the taskboard.
func (d *Daemon) markTaskCompleted(ctx context.Context, taskID string, exitCode int) {
	d.mu.Lock()

	d.board.Update(taskID, func(t *taskboard.Task) {
		now := time.Now().UTC()
		t.Status = taskboard.StatusCompleted
		t.CompletedAt = &now
		t.ExitCode = &exitCode
	})

	ready := d.board.EvaluateDeps()
	d.board.Save(d.boardPath)

	// Collect experts to wake before releasing the lock
	var expertsToWake []string
	if len(ready) > 0 {
		d.logger.Info("Dependencies resolved, tasks now ready",
			"completed_task", taskID,
			"newly_ready", ready,
		)
		seen := make(map[string]bool)
		for _, id := range ready {
			if t, ok := d.board.Get(id); ok && !seen[t.Expert] {
				seen[t.Expert] = true
				expertsToWake = append(expertsToWake, t.Expert)
				d.events.emit(Event{
					Type:      EventTaskUnblocked,
					Timestamp: time.Now(),
					Data:      TaskUnblockedData{TaskID: id, Expert: t.Expert},
				})
			}
		}
	}

	d.mu.Unlock()

	// Wake experts outside the lock — handleInbox acquires its own lock
	for _, expert := range expertsToWake {
		d.wg.Add(1)
		go func(e string) { defer d.wg.Done(); d.handleInbox(ctx, e, "") }(expert)
	}

	// Check curation threshold after task completion
	if d.curation.RecordTaskCompletion() {
		d.wg.Add(1)
		go func() { defer d.wg.Done(); d.triggerCuration("task_threshold") }()
	}
}

// markTaskFailed updates a task's status to failed, propagates failure to
// dependents, and saves the taskboard.
func (d *Daemon) markTaskFailed(taskID string, exitCode int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.board.Update(taskID, func(t *taskboard.Task) {
		now := time.Now().UTC()
		t.Status = taskboard.StatusFailed
		t.CompletedAt = &now
		t.ExitCode = &exitCode
	})

	d.board.EvaluateDeps()
	d.board.Save(d.boardPath)
}

// drainAllInboxes processes any files sitting in expert and architect inboxes
// when the daemon starts. Each drains in its own goroutine via handleInbox.
func (d *Daemon) drainAllInboxes(ctx context.Context) {
	// Drain built-in role inboxes
	d.wg.Add(1)
	go func() { defer d.wg.Done(); d.handleInbox(ctx, "architect", "") }()
	d.wg.Add(1)
	go func() { defer d.wg.Done(); d.handleInbox(ctx, "researcher", "") }()

	for name := range d.cfg.Experts {
		d.wg.Add(1)
		go func(n string) { defer d.wg.Done(); d.handleInbox(ctx, n, "") }(name)
	}

	// Drain shared expert inboxes
	for _, name := range d.cfg.Shared.Include {
		d.wg.Add(1)
		go func(n string) { defer d.wg.Done(); d.handleInbox(ctx, n, "") }(name)
	}
}

// drainInbox iteratively processes all .md files in an expert's inbox (oldest
// first). It reads the file list once and processes each in order. Files that
// fail to parse are skipped (logged and left in inbox for manual inspection).
func (d *Daemon) drainInbox(ctx context.Context, expertName string) {
	var inboxDir string
	if d.isSharedExpert(expertName) {
		inboxDir = mail.ResolveSharedInbox(d.poolDir, expertName)
	} else {
		inboxDir = mail.ResolveInbox(d.poolDir, expertName)
	}

	// Track files we've already attempted so we don't loop forever on
	// non-zero exit files (which are preserved in inbox).
	seen := make(map[string]bool)

	// Re-scan after each batch — messages may arrive while we process.
	// The loop exits when no unseen .md files remain (or context is cancelled).
	for {
		if ctx.Err() != nil {
			return
		}

		entries, err := os.ReadDir(inboxDir)
		if err != nil {
			d.logger.Error("Failed to read inbox directory",
				"expert", expertName,
				"dir", inboxDir,
				"error", err,
			)
			return
		}

		// Collect unseen .md files, sort by mod time (oldest first = FIFO)
		var mdFiles []string
		for _, entry := range entries {
			path := filepath.Join(inboxDir, entry.Name())
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") && !seen[path] {
				mdFiles = append(mdFiles, path)
			}
		}

		if len(mdFiles) == 0 {
			return
		}

		sort.Slice(mdFiles, func(i, j int) bool {
			infoI, _ := os.Stat(mdFiles[i])
			infoJ, _ := os.Stat(mdFiles[j])
			if infoI == nil || infoJ == nil {
				return false
			}
			return infoI.ModTime().Before(infoJ.ModTime())
		})

		// Process each file in order. Files that fail to parse are skipped
		// (left in inbox for manual inspection). Non-zero exits also leave
		// the file in place but the loop continues to newer items — we
		// prioritize forward progress over strict FIFO ordering.
		// Dead-letter handling is v0.3 scope (task board + dependencies).
		for _, path := range mdFiles {
			seen[path] = true
			d.processInboxMessage(ctx, expertName, path)
		}
	}
}

// drainPostoffice routes any pre-existing messages in the postoffice directory.
// Called at startup before drainAllInboxes to ensure unrouted messages get
// delivered before inbox processing begins.
func (d *Daemon) drainPostoffice(ctx context.Context) {
	postofficeDir := filepath.Join(d.poolDir, "postoffice")
	entries, err := os.ReadDir(postofficeDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		if strings.HasPrefix(entry.Name(), ".routing-") {
			continue
		}
		d.handlePostoffice(ctx, filepath.Join(postofficeDir, entry.Name()))
	}
}

// resolveExpertConfig returns the model and allowed tools for an expert,
// falling back to pool defaults for empty values. Built-in roles (architect)
// use their own config section.
func (d *Daemon) resolveExpertConfig(name string) (model string, tools []string) {
	model = d.cfg.Defaults.Model
	tools = d.cfg.Defaults.AllowedTools

	if name == "architect" {
		if d.cfg.Architect.Model != "" {
			model = d.cfg.Architect.Model
		}
		return model, tools
	}

	if name == "researcher" {
		if d.cfg.Researcher.Model != "" {
			model = d.cfg.Researcher.Model
		}
		return model, tools
	}

	if ec, ok := d.cfg.Experts[name]; ok {
		if ec.Model != "" {
			model = ec.Model
		}
		if len(ec.AllowedTools) > 0 {
			tools = ec.AllowedTools
		}
	}

	return model, tools
}

// handleApprovalRequest processes a .proposal.md file in the approvals directory.
// It reads the proposal, presents it to the human via the configured presenter,
// and writes a .approved or .rejected response. Runs in a goroutine so it doesn't
// block the event loop.
func (d *Daemon) handleApprovalRequest(ctx context.Context, path string) {
	filename := filepath.Base(path)
	proposalID := approval.ProposalID(filename)
	if proposalID == "" {
		return // not a .proposal.md file
	}

	if d.cfg.Architect.ApprovalMode == "none" {
		// Auto-approve in none mode
		approvalsDir := filepath.Join(d.poolDir, "approvals")
		if err := approval.Respond(approvalsDir, proposalID, true, ""); err != nil {
			d.logger.Error("Failed to auto-approve proposal",
				"proposal_id", proposalID,
				"error", err,
			)
		}
		return
	}

	approvalsDir := filepath.Join(d.poolDir, "approvals")
	proposal, err := approval.ReadProposal(approvalsDir, proposalID)
	if err != nil {
		d.logger.Error("Failed to read approval proposal",
			"proposal_id", proposalID,
			"error", err,
		)
		return
	}

	stdin := d.stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := d.stdout
	if stdout == nil {
		stdout = os.Stdout
	}

	presenter, err := approval.ParseHumanInbox(d.cfg.Architect.HumanInbox, stdin, stdout)
	if err != nil {
		d.logger.Error("Failed to parse human_inbox config",
			"value", d.cfg.Architect.HumanInbox,
			"error", err,
		)
		return
	}

	d.logger.Info("Presenting approval request to human",
		"proposal_id", proposalID,
		"mode", d.cfg.Architect.HumanInbox,
	)

	approved, err := presenter.Present(ctx, proposalID, proposal)
	if err != nil {
		d.logger.Error("Failed to present approval request",
			"proposal_id", proposalID,
			"error", err,
		)
		// Write rejection on presenter error so the tool handler unblocks
		if respondErr := approval.Respond(approvalsDir, proposalID, false, fmt.Sprintf("presenter error: %v", err)); respondErr != nil {
			d.logger.Error("Failed to write rejection after presenter error",
				"proposal_id", proposalID,
				"error", respondErr,
			)
		}
		return
	}

	reason := ""
	if !approved {
		reason = "human rejected"
	}

	if err := approval.Respond(approvalsDir, proposalID, approved, reason); err != nil {
		d.logger.Error("Failed to write approval response",
			"proposal_id", proposalID,
			"approved", approved,
			"error", err,
		)
		return
	}

	d.logger.Info("Successfully processed approval request",
		"proposal_id", proposalID,
		"approved", approved,
	)
}

// Status returns live daemon state for the socket status method.
func (d *Daemon) Status() map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()

	experts := make([]string, 0, len(d.cfg.Experts))
	for name := range d.cfg.Experts {
		experts = append(experts, name)
	}
	sort.Strings(experts)

	counts := make(map[string]int)
	var activeTasks []map[string]string
	for _, task := range d.board.Tasks {
		counts[string(task.Status)]++
		if task.Status == taskboard.StatusActive {
			entry := map[string]string{
				"id":     task.ID,
				"expert": task.Expert,
			}
			if task.StartedAt != nil {
				entry["started"] = time.Since(*task.StartedAt).Truncate(time.Second).String()
			}
			activeTasks = append(activeTasks, entry)
		}
	}

	return map[string]any{
		"pool":         d.cfg.Pool.Name,
		"state":        "running",
		"uptime":       time.Since(d.startedAt).Truncate(time.Second).String(),
		"experts":      experts,
		"task_counts":  counts,
		"active_tasks": activeTasks,
	}
}

// resolveSessionTimeout returns the session timeout for the given role or expert.
// Built-in roles with their own timeout config use that; otherwise falls back to defaults.
func (d *Daemon) resolveSessionTimeout(name string) (time.Duration, error) {
	if name == "architect" && d.cfg.Architect.SessionTimeout != "" {
		dur, err := time.ParseDuration(d.cfg.Architect.SessionTimeout)
		if err != nil {
			return 0, fmt.Errorf("parsing architect.session_timeout %q: %w", d.cfg.Architect.SessionTimeout, err)
		}
		return dur, nil
	}
	if name == "researcher" && d.cfg.Researcher.SessionTimeout != "" {
		dur, err := time.ParseDuration(d.cfg.Researcher.SessionTimeout)
		if err != nil {
			return 0, fmt.Errorf("parsing researcher.session_timeout %q: %w", d.cfg.Researcher.SessionTimeout, err)
		}
		return dur, nil
	}
	return d.cfg.Defaults.ParseSessionTimeout()
}

// resolveSockPath returns the unix socket path for CLI→daemon communication.
// Uses the override if set, otherwise delegates to config.ResolveSockPath
// (shared with the CLI to ensure both sides agree on the path).
func (d *Daemon) resolveSockPath() string {
	if d.sockPathOver != "" {
		return d.sockPathOver
	}
	return config.ResolveSockPath(d.poolDir)
}

// resolveExpertDir returns the state directory for an expert or built-in role.
func (d *Daemon) resolveExpertDir(name string) string {
	return mail.ResolveExpertDir(d.poolDir, name)
}

// resolveProjectDir expands ~ in the pool's project directory setting.
func (d *Daemon) resolveProjectDir() string {
	dir := d.cfg.Pool.ProjectDir
	if strings.HasPrefix(dir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, dir[2:])
		}
	}
	return dir
}

// resolveExpertName extracts the expert name from an inbox directory path.
// Checks built-in roles first, then pool-scoped experts.
func (d *Daemon) resolveExpertName(inboxDir string) string {
	// Check built-in roles first
	for _, role := range []string{"architect", "researcher"} {
		roleInbox := mail.ResolveInbox(d.poolDir, role)
		if absEqual(inboxDir, roleInbox) {
			return role
		}
	}

	for name := range d.cfg.Experts {
		expected := mail.ResolveInbox(d.poolDir, name)
		if absEqual(inboxDir, expected) {
			return name
		}
	}

	// Check shared expert inboxes
	for _, name := range d.cfg.Shared.Include {
		expected := mail.ResolveSharedInbox(d.poolDir, name)
		if absEqual(inboxDir, expected) {
			return name
		}
	}

	return ""
}

// ensureDirs creates the required directory structure for the pool.
func (d *Daemon) ensureDirs() error {
	dirs := []string{
		filepath.Join(d.poolDir, "postoffice"),
		filepath.Join(d.poolDir, "contracts"),
		filepath.Join(d.poolDir, "approvals"),
		// Built-in roles get top-level inbox + logs directories
		filepath.Join(d.poolDir, "architect", "inbox"),
		filepath.Join(d.poolDir, "architect", "logs"),
		filepath.Join(d.poolDir, "architect", "verifications"),
		filepath.Join(d.poolDir, "researcher", "inbox"),
		filepath.Join(d.poolDir, "researcher", "logs"),
		filepath.Join(d.poolDir, "concierge", "inbox"),
	}

	for name := range d.cfg.Experts {
		expertBase := filepath.Join(d.poolDir, "experts", name)
		dirs = append(dirs,
			filepath.Join(expertBase, "inbox"),
			filepath.Join(expertBase, "logs"),
		)
	}

	// Shared experts get pool-scoped shared-state directories for inbox and logs.
	// Identity and user-level state live at ~/.agent-pool/experts/{name}/ (not created here).
	for _, name := range d.cfg.Shared.Include {
		dirs = append(dirs,
			mail.ResolveSharedInbox(d.poolDir, name),
			mail.ResolveSharedLogDir(d.poolDir, name),
		)
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}

	// Warn if shared expert user-level directory is missing identity.md
	for _, name := range d.cfg.Shared.Include {
		userDir, resolveErr := mail.ResolveSharedExpertDir(name)
		if resolveErr != nil {
			continue
		}
		identityPath := filepath.Join(userDir, "identity.md")
		if _, err := os.Stat(identityPath); os.IsNotExist(err) {
			d.logger.Warn("Shared expert missing identity.md at user level",
				"expert", name,
				"expected", identityPath,
			)
		}
	}

	return nil
}

// sharedNamesMap returns the cached shared expert lookup set. May be nil.
func (d *Daemon) sharedNamesMap() map[string]bool {
	return d.sharedSet
}

// isSharedExpert reports whether the named expert is in the pool's shared.include list.
func (d *Daemon) isSharedExpert(name string) bool {
	return d.sharedSet[name]
}

// absEqual compares two paths after resolving to absolute form.
func absEqual(a, b string) bool {
	absA, _ := filepath.Abs(a)
	absB, _ := filepath.Abs(b)
	return absA == absB
}
