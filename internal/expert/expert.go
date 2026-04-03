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
	"time"

	"git.sjo.lol/cameron/agent-pool/internal/mail"
)

// SpawnConfig holds everything needed to spawn an expert session.
type SpawnConfig struct {
	Name         string
	Model        string
	AllowedTools []string
	ProjectDir   string        // working directory for claude
	ExpertDir    string        // contains identity.md, state.md, errors.md
	TaskMessage  *mail.Message // the task to execute
}

// Result holds the outcome of an expert session.
type Result struct {
	TaskID   string
	ExitCode int
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

	var b strings.Builder

	// Identity
	if content, err := readOptionalFile(cfg.ExpertDir, "identity.md"); err != nil {
		return "", fmt.Errorf("reading identity.md: %w", err)
	} else if content != "" {
		b.WriteString("## Expert Identity\n\n")
		b.WriteString(content)
		b.WriteString("\n\n")
	}

	// State
	if content, err := readOptionalFile(cfg.ExpertDir, "state.md"); err != nil {
		return "", fmt.Errorf("reading state.md: %w", err)
	} else if content != "" {
		b.WriteString("## Current State\n\n")
		b.WriteString(content)
		b.WriteString("\n\n")
	}

	// Errors
	if content, err := readOptionalFile(cfg.ExpertDir, "errors.md"); err != nil {
		return "", fmt.Errorf("reading errors.md: %w", err)
	} else if content != "" {
		b.WriteString("## Known Errors & Pitfalls\n\n")
		b.WriteString(content)
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

	cmd := exec.CommandContext(ctx, claudePath, args...)
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Dir = cfg.ProjectDir

	// Inherit current env + pool-specific vars
	cmd.Env = append(os.Environ(),
		"AGENT_POOL_EXPERT="+cfg.Name,
		"AGENT_POOL_TASK_ID="+cfg.TaskMessage.ID,
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
	runErr := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("running claude: %w", runErr)
		}
	}

	result := &Result{
		TaskID:   cfg.TaskMessage.ID,
		ExitCode: exitCode,
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
