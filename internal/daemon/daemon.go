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

// Daemon is the agent-pool process supervisor.
type Daemon struct {
	cfg     *config.PoolConfig
	poolDir string
	logger  *slog.Logger

	mu   sync.Mutex
	busy map[string]bool // tracks which experts are currently spawned
}

// New creates a Daemon for the given pool.
func New(cfg *config.PoolConfig, poolDir string, logger *slog.Logger) *Daemon {
	return &Daemon{
		cfg:     cfg,
		poolDir: poolDir,
		logger:  logger,
		busy:    make(map[string]bool),
	}
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

	d.logger.Info("Daemon started",
		"pool", d.cfg.Pool.Name,
		"pool_dir", d.poolDir,
		"experts", len(d.cfg.Experts),
	)

	// Drain any existing inbox files from before the daemon started
	d.drainAllInboxes(ctx)

	// Main event loop
	for {
		select {
		case <-ctx.Done():
			d.logger.Info("Daemon shutting down")
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
					d.handleInbox(ctx, expertName, event.Path)
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

	d.logger.Info("Message routed",
		"id", msg.ID,
		"to", msg.To,
	)
}

// handleInbox spawns an expert session for a message in an inbox.
func (d *Daemon) handleInbox(ctx context.Context, expertName string, path string) {
	d.mu.Lock()
	if d.busy[expertName] {
		d.mu.Unlock()
		d.logger.Info("Expert busy, message queued",
			"expert", expertName,
			"path", path,
		)
		return
	}
	d.busy[expertName] = true
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		d.busy[expertName] = false
		d.mu.Unlock()

		// Drain remaining inbox files after this spawn completes
		d.drainInbox(ctx, expertName)
	}()

	msg, err := mail.ParseFile(path)
	if err != nil {
		d.logger.Error("Failed to parse inbox message",
			"expert", expertName,
			"path", path,
			"error", err,
		)
		return
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

	result, err := expert.Spawn(ctx, d.logger, cfg)
	if err != nil {
		d.logger.Error("Failed to spawn expert",
			"expert", expertName,
			"task_id", msg.ID,
			"error", err,
		)
		return
	}

	// Write logs
	if err := expert.WriteLog(expertDir, result.TaskID, result.Output); err != nil {
		d.logger.Error("Failed to write task log",
			"expert", expertName,
			"task_id", result.TaskID,
			"error", err,
		)
	}

	if err := expert.AppendIndex(expertDir, &expert.LogEntry{
		TaskID:    result.TaskID,
		Timestamp: msg.Timestamp,
		From:      msg.From,
		Summary:   result.Summary,
	}); err != nil {
		d.logger.Error("Failed to append log index",
			"expert", expertName,
			"task_id", result.TaskID,
			"error", err,
		)
	}

	// Remove processed inbox file
	if err := os.Remove(path); err != nil {
		d.logger.Warn("Failed to remove processed inbox file",
			"path", path,
			"error", err,
		)
	}

	d.logger.Info("Task completed",
		"expert", expertName,
		"task_id", result.TaskID,
		"exit_code", result.ExitCode,
		"duration", result.Duration,
		"summary", result.Summary,
	)
}

// drainAllInboxes processes any files sitting in expert inboxes when the daemon starts.
func (d *Daemon) drainAllInboxes(ctx context.Context) {
	for name := range d.cfg.Experts {
		d.drainInbox(ctx, name)
	}
}

// drainInbox processes the oldest unprocessed file in an expert's inbox.
// Called after each spawn completes and on startup to catch files that arrived
// while the daemon was down or an expert was busy.
func (d *Daemon) drainInbox(ctx context.Context, expertName string) {
	inboxDir := mail.ResolveInbox(d.poolDir, expertName)

	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		return
	}

	// Collect .md files, sort by mod time (oldest first = FIFO)
	var mdFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
			mdFiles = append(mdFiles, filepath.Join(inboxDir, entry.Name()))
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

	// Process the oldest one — handleInbox will recurse via drainInbox
	d.handleInbox(ctx, expertName, mdFiles[0])
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
