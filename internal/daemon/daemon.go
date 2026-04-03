// Package daemon implements the core agent-pool process supervisor.
//
// The daemon watches pool directories for mail delivery via fsnotify,
// routes messages between agents, spawns Claude Code sessions for experts,
// and manages the task lifecycle.
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"git.sjo.lol/cameron/agent-pool/internal/config"
	"git.sjo.lol/cameron/agent-pool/internal/expert"
	"git.sjo.lol/cameron/agent-pool/internal/mail"
	agentmcp "git.sjo.lol/cameron/agent-pool/internal/mcp"
	"git.sjo.lol/cameron/agent-pool/internal/taskboard"
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

	mu        sync.Mutex
	board     *taskboard.Board
	boardPath string
	draining  map[string]bool // re-entrancy guard for expert inbox drains
}

// Option configures a Daemon.
type Option func(*Daemon)

// WithSpawner sets a custom spawner (used in tests).
func WithSpawner(s Spawner) Option {
	return func(d *Daemon) { d.spawner = s }
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
		cfg:       cfg,
		poolDir:   poolDir,
		logger:    logger,
		board:     board,
		boardPath: boardPath,
		draining:  make(map[string]bool),
		spawner:   defaultSpawner{},
	}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Run starts the daemon's main loop. It blocks until ctx is cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	if err := d.ensureDirs(); err != nil {
		return fmt.Errorf("ensuring directory structure: %w", err)
	}

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

	// Watch each expert's inbox
	for name := range d.cfg.Experts {
		inboxDir := mail.ResolveInbox(d.poolDir, name)
		if err := watcher.Add(inboxDir); err != nil {
			return fmt.Errorf("watching inbox for %s: %w", name, err)
		}
	}

	// Start watcher goroutine
	go watcher.Run(ctx)

	d.logger.Info("Successfully started daemon",
		"pool", d.cfg.Pool.Name,
		"pool_dir", d.poolDir,
		"experts", len(d.cfg.Experts),
	)

	// Drain pre-existing messages from before the daemon started
	d.drainPostoffice(ctx)
	d.drainAllInboxes(ctx)

	// Main event loop
	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Shutting down daemon",
				"pool", d.cfg.Pool.Name,
			)
			return nil

		case event, ok := <-watcher.Events():
			if !ok {
				return nil
			}

			if event.Dir == postofficeDir {
				d.handlePostoffice(ctx, event.Path)
			} else {
				// Determine which expert this inbox belongs to
				expertName := d.resolveExpertName(event.Dir)
				if expertName != "" {
					// Dispatch in a goroutine so expert spawns don't
					// block postoffice routing or other experts.
					// The busy flag inside handleInbox prevents
					// concurrent spawns for the same expert.
					go d.handleInbox(ctx, expertName, event.Path)
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
	routed, err := mail.Route(d.logger, d.poolDir, path)
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
		inboxPath := filepath.Join(mail.ResolveInbox(d.poolDir, task.Expert), targetID+".md")
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
		d.logger.Debug("Skipping expert dispatch. Reason: expert busy",
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
	d.ensureTaskRegistered(msg)

	// Check taskboard status under lock — skip blocked or cancelled tasks.
	d.mu.Lock()
	task, tracked := d.board.Get(msg.ID)
	if tracked {
		switch task.Status {
		case taskboard.StatusBlocked:
			d.mu.Unlock()
			d.logger.Debug("Skipping task. Reason: blocked on dependencies",
				"expert", expertName,
				"task_id", msg.ID,
			)
			return false
		case taskboard.StatusCancelled:
			d.mu.Unlock()
			d.logger.Debug("Skipping task. Reason: task was cancelled",
				"expert", expertName,
				"task_id", msg.ID,
			)
			os.Remove(path)
			return true
		case taskboard.StatusCompleted, taskboard.StatusFailed:
			d.mu.Unlock()
			d.logger.Debug("Skipping task. Reason: task already reached terminal status",
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

		d.logger.Debug("Preparing to run task",
			"expert", expertName,
			"task_id", msg.ID,
		)
	}
	d.mu.Unlock()

	model, tools := d.resolveExpertConfig(expertName)
	projectDir := d.resolveProjectDir()
	expertDir := filepath.Join(d.poolDir, "experts", expertName)

	mcpConfigPath, mcpErr := agentmcp.WriteTempConfig(d.poolDir, expertName)
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

	cfg := &expert.SpawnConfig{
		Name:          expertName,
		Model:         model,
		AllowedTools:  tools,
		ProjectDir:    projectDir,
		ExpertDir:     expertDir,
		PoolDir:       d.poolDir,
		TaskMessage:   msg,
		MCPConfigPath: mcpConfigPath,
	}

	timeout, parseErr := d.cfg.Defaults.ParseSessionTimeout()
	if parseErr != nil {
		d.logger.Warn("Failed to parse session timeout, using default 10m",
			"error", parseErr,
		)
		timeout = 10 * time.Minute
	}
	spawnCtx, spawnCancel := context.WithTimeout(ctx, timeout)
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

	// Always write logs — the archive is append-only by design
	if err := expert.WriteLog(expertDir, result.TaskID, result.Output); err != nil {
		d.logger.Error("Failed to write task log",
			"expert", expertName,
			"task_id", result.TaskID,
			"error", err,
		)
	}

	if len(result.Stderr) > 0 {
		if err := expert.WriteStderr(expertDir, result.TaskID, result.Stderr); err != nil {
			d.logger.Error("Failed to write stderr log",
				"expert", expertName,
				"task_id", result.TaskID,
				"error", err,
			)
		}
	}

	if err := expert.AppendIndex(expertDir, &expert.LogEntry{
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

	d.markTaskCompleted(msg.ID, result.ExitCode)

	d.logger.Info("Successfully completed task",
		"expert", expertName,
		"task_id", result.TaskID,
		"exit_code", result.ExitCode,
		"duration", result.Duration,
		"summary", result.Summary,
	)

	return true
}

// ensureTaskRegistered adds a task to the taskboard if it isn't already tracked.
// This handles pre-existing inbox files from before the daemon started.
func (d *Daemon) ensureTaskRegistered(msg *mail.Message) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.board.Get(msg.ID); ok {
		return
	}

	status := taskboard.StatusPending
	if len(msg.DependsOn) > 0 {
		status = taskboard.StatusBlocked
	}

	d.board.Add(&taskboard.Task{
		ID:        msg.ID,
		Status:    status,
		Expert:    msg.To,
		DependsOn: msg.DependsOn,
		From:      msg.From,
		Type:      string(msg.Type),
		Priority:  string(msg.Priority),
	})
	d.board.Save(d.boardPath)
}

// markTaskCompleted updates a task's status to completed, evaluates dependencies,
// and saves the taskboard.
func (d *Daemon) markTaskCompleted(taskID string, exitCode int) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.board.Update(taskID, func(t *taskboard.Task) {
		now := time.Now().UTC()
		t.Status = taskboard.StatusCompleted
		t.CompletedAt = &now
		t.ExitCode = &exitCode
	})

	ready := d.board.EvaluateDeps()
	if len(ready) > 0 {
		d.logger.Info("Dependencies resolved, tasks now ready",
			"completed_task", taskID,
			"newly_ready", ready,
		)
	}

	d.board.Save(d.boardPath)
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

// drainAllInboxes processes any files sitting in expert inboxes when the daemon starts.
// Each expert drains in its own goroutine via handleInbox so they run concurrently.
func (d *Daemon) drainAllInboxes(ctx context.Context) {
	for name := range d.cfg.Experts {
		go d.handleInbox(ctx, name, "")
	}
}

// drainInbox iteratively processes all .md files in an expert's inbox (oldest
// first). It reads the file list once and processes each in order. Files that
// fail to parse are skipped (logged and left in inbox for manual inspection).
func (d *Daemon) drainInbox(ctx context.Context, expertName string) {
	inboxDir := mail.ResolveInbox(d.poolDir, expertName)

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
// falling back to pool defaults for empty values.
func (d *Daemon) resolveExpertConfig(name string) (model string, tools []string) {
	model = d.cfg.Defaults.Model
	tools = d.cfg.Defaults.AllowedTools

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
func (d *Daemon) resolveExpertName(inboxDir string) string {
	for name := range d.cfg.Experts {
		expected := mail.ResolveInbox(d.poolDir, name)
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
		// Built-in roles get top-level inbox directories
		filepath.Join(d.poolDir, "architect", "inbox"),
		filepath.Join(d.poolDir, "researcher", "inbox"),
		filepath.Join(d.poolDir, "concierge", "inbox"),
	}

	for name := range d.cfg.Experts {
		expertBase := filepath.Join(d.poolDir, "experts", name)
		dirs = append(dirs,
			filepath.Join(expertBase, "inbox"),
			filepath.Join(expertBase, "logs"),
		)
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}

	return nil
}

// absEqual compares two paths after resolving to absolute form.
func absEqual(a, b string) bool {
	absA, _ := filepath.Abs(a)
	absB, _ := filepath.Abs(b)
	return absA == absB
}
