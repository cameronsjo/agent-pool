// Package expert handles spawning, lifecycle management, and state assembly
// for headless Claude Code expert sessions.
//
// Each expert invocation is a fresh "claude -p" call with a prompt assembled
// from identity.md + state.md + errors.md + the task. The session is
// disposable; the knowledge persists on disk.
package expert

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cameronsjo/agent-pool/internal/mail"
)

// SpawnConfig holds everything needed to spawn an expert session.
type SpawnConfig struct {
	Name          string
	Model         string
	AllowedTools  []string
	ProjectDir    string        // working directory for claude
	ExpertDir     string        // contains identity.md, state.md, errors.md
	PoolDir       string        // pool root directory (set as AGENT_POOL_DIR env var)
	TaskMessage   *mail.Message // the task to execute
	MCPConfigPath string        // path to MCP config JSON; if set, --mcp-config is added
}

// Result holds the outcome of an expert session.
type Result struct {
	TaskID   string
	ExitCode int
	PID      int
	Output   []byte // raw stream-json output from stdout
	Stderr   []byte
	Summary  string
	Duration time.Duration
}

// AssemblePrompt reads state files and the task, then builds the full prompt.
//
// Sections are included in order: identity, state, errors, task.
// Missing state files are silently skipped — a new expert may not have all files yet.
func AssemblePrompt(cfg *SpawnConfig) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("spawn config is nil")
	}

	identity, state, errors, err := ReadState(cfg.ExpertDir)
	if err != nil {
		return "", err
	}

	var b strings.Builder

	if identity != "" {
		b.WriteString("## Expert Identity\n\n")
		b.WriteString(identity)
		b.WriteString("\n\n")
	}

	if state != "" {
		b.WriteString("## Current State\n\n")
		b.WriteString(state)
		b.WriteString("\n\n")
	}

	if errors != "" {
		b.WriteString("## Known Errors & Pitfalls\n\n")
		b.WriteString(errors)
		b.WriteString("\n\n")
	}

	// Task
	if cfg.TaskMessage == nil {
		return "", fmt.Errorf("no task message provided")
	}

	b.WriteString("## Task\n\n")
	b.WriteString(cfg.TaskMessage.Body)
	b.WriteString("\n\n")

	// Task metadata
	b.WriteString("### Task Metadata\n")
	fmt.Fprintf(&b, "- ID: %s\n", cfg.TaskMessage.ID)
	fmt.Fprintf(&b, "- From: %s\n", cfg.TaskMessage.From)
	fmt.Fprintf(&b, "- Type: %s\n", cfg.TaskMessage.Type)
	fmt.Fprintf(&b, "- Priority: %s\n", cfg.TaskMessage.Priority)

	contracts := "none"
	if len(cfg.TaskMessage.Contracts) > 0 {
		contracts = strings.Join(cfg.TaskMessage.Contracts, ", ")
	}
	fmt.Fprintf(&b, "- Contracts: %s\n", contracts)

	return b.String(), nil
}

// Spawn starts a claude -p session and blocks until it exits.
//
// The assembled prompt is written to stdin. Stdout (stream-json) and stderr
// are captured separately. Environment variables AGENT_POOL_EXPERT and
// AGENT_POOL_TASK_ID are set for the session.
func Spawn(ctx context.Context, logger *slog.Logger, cfg *SpawnConfig) (*Result, error) {
	if cfg == nil {
		return nil, fmt.Errorf("spawn config is nil")
	}
	if cfg.PoolDir == "" {
		return nil, fmt.Errorf("pool directory is required")
	}

	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return nil, fmt.Errorf("claude binary not found in PATH: %w", err)
	}

	prompt, err := AssemblePrompt(cfg)
	if err != nil {
		return nil, fmt.Errorf("assembling prompt: %w", err)
	}

	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--model", cfg.Model,
	}

	if len(cfg.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(cfg.AllowedTools, ","))
	}

	if cfg.MCPConfigPath != "" {
		args = append(args, "--mcp-config", cfg.MCPConfigPath)
	}

	// Use exec.Command (not CommandContext) so we control shutdown signals
	// ourselves. CommandContext sends SIGKILL immediately, racing our SIGTERM
	// grace period.
	cmd := exec.Command(claudePath, args...)
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Dir = cfg.ProjectDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Inherit current env + pool-specific vars
	cmd.Env = append(os.Environ(),
		"AGENT_POOL_EXPERT="+cfg.Name,
		"AGENT_POOL_TASK_ID="+cfg.TaskMessage.ID,
		"AGENT_POOL_DIR="+cfg.PoolDir,
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	logger.Debug("Preparing to spawn expert session",
		"expert", cfg.Name,
		"task_id", cfg.TaskMessage.ID,
		"model", cfg.Model,
		"project_dir", cfg.ProjectDir,
	)

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting claude: %w", err)
	}

	pid := 0
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var runErr error
	cancelled := false
	select {
	case runErr = <-done:
		// Normal exit
	case <-ctx.Done():
		cancelled = true
		// Timeout or parent cancel — send SIGTERM to process group for graceful shutdown
		logger.Warn("Session context cancelled, sending SIGTERM",
			"expert", cfg.Name,
			"task_id", cfg.TaskMessage.ID,
			"pid", pid,
			"reason", ctx.Err(),
		)
		if pid > 0 {
			_ = syscall.Kill(-pid, syscall.SIGTERM)
		}
		// Grace period for Stop hook to flush state
		select {
		case runErr = <-done:
			logger.Debug("Expert process exited after SIGTERM",
				"expert", cfg.Name,
				"task_id", cfg.TaskMessage.ID,
				"pid", pid,
			)
		case <-time.After(10 * time.Second):
			logger.Warn("Expert process did not exit after SIGTERM, sending SIGKILL",
				"expert", cfg.Name,
				"task_id", cfg.TaskMessage.ID,
				"pid", pid,
			)
			if pid > 0 {
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
			runErr = <-done
		}
	}

	duration := time.Since(start)

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("running claude: %w", runErr)
		}
	}
	// Cancelled sessions always get non-zero exit code, even if the process
	// exited cleanly after SIGTERM — this ensures the inbox file is preserved
	// for retry and the daemon marks the task as failed.
	if cancelled && exitCode == 0 {
		exitCode = -1
	}

	result := &Result{
		TaskID:   cfg.TaskMessage.ID,
		ExitCode: exitCode,
		PID:      pid,
		Output:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
		Duration: duration,
	}

	result.Summary = ExtractSummary(result.Output)

	if exitCode == 0 {
		logger.Info("Successfully completed expert session",
			"expert", cfg.Name,
			"task_id", cfg.TaskMessage.ID,
			"duration", duration,
			"output_bytes", len(result.Output),
		)
	} else {
		logger.Warn("Expert session exited with non-zero code",
			"expert", cfg.Name,
			"task_id", cfg.TaskMessage.ID,
			"exit_code", exitCode,
			"duration", duration,
			"output_bytes", len(result.Output),
		)
	}

	if len(stderr.Bytes()) > 0 {
		logger.Warn("Expert session produced stderr output",
			"expert", cfg.Name,
			"task_id", cfg.TaskMessage.ID,
			"stderr_bytes", len(stderr.Bytes()),
		)
	}

	return result, nil
}

// readOptionalFile reads a file from a directory. Returns empty string if the
// file does not exist. Returns an error only for unexpected read failures.
func readOptionalFile(dir, name string) (string, error) {
	path := filepath.Join(dir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
