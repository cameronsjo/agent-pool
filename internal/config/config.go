// Package config handles pool.toml parsing and validation.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// PoolConfig is the top-level pool.toml structure.
type PoolConfig struct {
	Pool       PoolSection              `toml:"pool"`
	Concierge  RoleSection              `toml:"concierge"`
	Architect  ArchitectSection         `toml:"architect"`
	Researcher RoleSection              `toml:"researcher"`
	Shared     SharedSection            `toml:"shared"`
	Defaults   DefaultsSection          `toml:"defaults"`
	Curation   CurationSection          `toml:"curation"`
	Experts    map[string]ExpertSection `toml:"experts"`
}

// PoolSection identifies the pool and its project.
type PoolSection struct {
	Name       string `toml:"name"`
	ProjectDir string `toml:"project_dir"`
}

// RoleSection is the common config for concierge/researcher.
type RoleSection struct {
	Model          string `toml:"model"`
	SessionTimeout string `toml:"session_timeout"`
}

// ArchitectSection extends RoleSection with approval settings.
type ArchitectSection struct {
	Model          string `toml:"model"`
	SessionTimeout string `toml:"session_timeout"`
	ApprovalMode   string `toml:"approval_mode"` // "none" | "decomposition" | "all"
	HumanInbox     string `toml:"human_inbox"`    // "stdout" | "telegram" | "file:~/reviews/"
}

// SharedSection declares which shared experts this pool includes.
type SharedSection struct {
	Include []string `toml:"include"`
}

// DefaultsSection provides fallback values for experts.
type DefaultsSection struct {
	Model          string   `toml:"model"`
	AllowedTools   []string `toml:"allowed_tools"`
	SessionTimeout string   `toml:"session_timeout"`
}

// CurationSection controls the researcher's curation schedule.
type CurationSection struct {
	IntervalTasks int `toml:"interval_tasks"`
	IntervalHours int `toml:"interval_hours"`
}

// ParseSessionTimeout parses the session timeout string to a time.Duration.
func (d DefaultsSection) ParseSessionTimeout() (time.Duration, error) {
	return time.ParseDuration(d.SessionTimeout)
}

// ExpertSection is the per-expert config.
type ExpertSection struct {
	Model        string   `toml:"model"`
	AllowedTools []string `toml:"allowed_tools"`
}

// LoadPool reads and parses a pool.toml from the given directory.
// If poolDir is empty, it uses the current working directory.
func LoadPool(poolDir string) (*PoolConfig, error) {
	if poolDir == "" {
		var err error
		poolDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("getting working directory: %w", err)
		}
	}

	// Expand ~ to home directory
	if len(poolDir) > 0 && poolDir[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("expanding home directory: %w", err)
		}
		poolDir = filepath.Join(home, poolDir[1:])
	}

	configPath := filepath.Join(poolDir, "pool.toml")

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", configPath, err)
	}

	var cfg PoolConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", configPath, err)
	}

	// Apply defaults
	if cfg.Defaults.Model == "" {
		cfg.Defaults.Model = "sonnet"
	}
	if cfg.Defaults.SessionTimeout == "" {
		cfg.Defaults.SessionTimeout = "10m"
	}
	if len(cfg.Defaults.AllowedTools) == 0 {
		cfg.Defaults.AllowedTools = []string{"Read", "Write", "Edit", "Bash", "Grep", "Glob"}
	}
	if cfg.Architect.ApprovalMode == "" {
		cfg.Architect.ApprovalMode = "decomposition"
	}
	if cfg.Architect.HumanInbox == "" {
		cfg.Architect.HumanInbox = "stdout"
	}
	if cfg.Curation.IntervalTasks == 0 {
		cfg.Curation.IntervalTasks = 10
	}
	if cfg.Curation.IntervalHours == 0 {
		cfg.Curation.IntervalHours = 168
	}

	return &cfg, nil
}
