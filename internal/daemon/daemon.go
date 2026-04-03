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

	"git.sjo.lol/cameron/agent-pool/internal/config"
	"git.sjo.lol/cameron/agent-pool/internal/expert"
	"git.sjo.lol/cameron/agent-pool/internal/mail"
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

	mu   sync.Mutex
	busy map[string]bool // tracks which experts are currently spawned
}

// Option configures a Daemon.
type Option func(*Daemon)

// WithSpawner sets a custom spawner (used in tests).
func WithSpawner(s Spawner) Option {
	return func(d *Daemon) { d.spawner = s }
}

// New creates a Daemon for the given pool.
func New(cfg *config.PoolConfig, poolDir string, logger *slog.Logger, opts ...Option) *Daemon {
	d := &Daemon{
		cfg:     cfg,
		poolDir: poolDir,
		logger:  logger,
		busy:    make(map[string]bool),
		spawner: defaultSpawner{},
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
			d.logger.Info("Preparing to shut down daemon")
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

// handlePostoffice routes a message from the postoffice to the recipient's inbox.
func (d *Daemon) handlePostoffice(ctx context.Context, path string) {
	msg, err := mail.Route(d.logger, d.poolDir, path)
	if err != nil {
		d.logger.Error("Failed to route message",
			"path", path,
			"error", err,
		)
		return
	}

	d.logger.Info("Successfully routed message",
		"id", msg.ID,
		"to", msg.To,
	)
}

// handleInbox is the entry point from the watcher event loop. It acquires the
// busy flag for the expert, drains all queued inbox messages iteratively, then
// releases the flag.
func (d *Daemon) handleInbox(ctx context.Context, expertName string, _ string) {
	d.mu.Lock()
	if d.busy[expertName] {
		d.mu.Unlock()
		d.logger.Debug("Skipping expert dispatch. Reason: expert busy",
			"expert", expertName,
		)
		return
	}
	d.busy[expertName] = true
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		d.busy[expertName] = false
		d.mu.Unlock()
	}()

	d.drainInbox(ctx, expertName)
}

// processInboxMessage handles a single inbox file: parse, spawn, log, and
// conditionally remove. Returns true if the file was successfully processed
// (regardless of exit code), false if parsing or spawning failed.
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

	model, tools := d.resolveExpertConfig(expertName)
	projectDir := d.resolveProjectDir()
	expertDir := filepath.Join(d.poolDir, "experts", expertName)

	cfg := &expert.SpawnConfig{
		Name:         expertName,
		Model:        model,
		AllowedTools: tools,
		ProjectDir:   projectDir,
		ExpertDir:    expertDir,
		TaskMessage:  msg,
	}

	result, err := d.spawner.Spawn(ctx, d.logger, cfg)
	if err != nil {
		d.logger.Error("Failed to spawn expert",
			"expert", expertName,
			"task_id", msg.ID,
			"error", err,
		)
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
		return true
	}

	// Remove inbox file only on success
	if err := os.Remove(path); err != nil {
		d.logger.Warn("Failed to remove processed inbox file",
			"path", path,
			"error", err,
		)
	}

	d.logger.Info("Successfully completed task",
		"expert", expertName,
		"task_id", result.TaskID,
		"exit_code", result.ExitCode,
		"duration", result.Duration,
		"summary", result.Summary,
	)

	return true
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
