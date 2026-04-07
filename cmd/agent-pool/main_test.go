// Test plan for main.go:
//
// parseFlagsFromArgs:
//   - All flags present → returns all values
//   - Some flags missing → missing flags return empty string
//   - Empty args → returns empty map
//   - Three flags → all parsed correctly
//   - Unknown flags → ignored
//   - Flag at end with no value → ignored
//   - Repeated flag → last value wins
//
// initPool:
//   - Fresh directory → creates dirs + pool.toml with correct content
//   - Already exists → returns error
//   - Generated TOML is parseable by config.LoadPool

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cameronsjo/agent-pool/internal/config"
)

func TestParseFlagsFromArgs_AllPresent(t *testing.T) {
	args := []string{"--pool", "/tmp/pool", "--expert", "auth"}
	result := parseFlagsFromArgs(args, "pool", "expert")

	if result["pool"] != "/tmp/pool" {
		t.Errorf("pool = %q, want %q", result["pool"], "/tmp/pool")
	}
	if result["expert"] != "auth" {
		t.Errorf("expert = %q, want %q", result["expert"], "auth")
	}
}

func TestParseFlagsFromArgs_SomeMissing(t *testing.T) {
	args := []string{"--pool", "/tmp/pool"}
	result := parseFlagsFromArgs(args, "pool", "expert")

	if result["pool"] != "/tmp/pool" {
		t.Errorf("pool = %q, want %q", result["pool"], "/tmp/pool")
	}
	if result["expert"] != "" {
		t.Errorf("expert = %q, want empty string", result["expert"])
	}
}

func TestParseFlagsFromArgs_EmptyArgs(t *testing.T) {
	result := parseFlagsFromArgs(nil, "pool", "expert")

	if len(result) != 0 {
		t.Errorf("expected empty map, got %v", result)
	}
}

func TestParseFlagsFromArgs_ThreeFlags(t *testing.T) {
	args := []string{"--pool", "/tmp/pool", "--expert", "auth", "--task", "task-001"}
	result := parseFlagsFromArgs(args, "pool", "expert", "task")

	if result["pool"] != "/tmp/pool" {
		t.Errorf("pool = %q, want %q", result["pool"], "/tmp/pool")
	}
	if result["expert"] != "auth" {
		t.Errorf("expert = %q, want %q", result["expert"], "auth")
	}
	if result["task"] != "task-001" {
		t.Errorf("task = %q, want %q", result["task"], "task-001")
	}
}

func TestParseFlagsFromArgs_UnknownFlagsIgnored(t *testing.T) {
	args := []string{"--unknown", "value", "--pool", "/tmp/pool"}
	result := parseFlagsFromArgs(args, "pool")

	if result["pool"] != "/tmp/pool" {
		t.Errorf("pool = %q, want %q", result["pool"], "/tmp/pool")
	}
	if _, ok := result["unknown"]; ok {
		t.Error("unexpected 'unknown' key in result")
	}
}

func TestParseFlagsFromArgs_FlagAtEnd(t *testing.T) {
	// Flag name at the end with no value should be ignored
	args := []string{"--pool"}
	result := parseFlagsFromArgs(args, "pool")

	if result["pool"] != "" {
		t.Errorf("pool = %q, want empty (no value after flag)", result["pool"])
	}
}

func TestParseFlagsFromArgs_RepeatedFlag(t *testing.T) {
	// Last value wins
	args := []string{"--pool", "first", "--pool", "second"}
	result := parseFlagsFromArgs(args, "pool")

	if result["pool"] != "second" {
		t.Errorf("pool = %q, want %q (last value wins)", result["pool"], "second")
	}
}

func TestInitPool_CreatesStructure(t *testing.T) {
	tmp := t.TempDir()
	poolDir := filepath.Join(tmp, ".agent-pool")

	err := initPool(poolDir, "my-project", "/home/user/my-project")
	if err != nil {
		t.Fatalf("initPool: %v", err)
	}

	// Verify directories
	expectedDirs := []string{
		"postoffice",
		"contracts",
		"formulas",
		"architect/inbox",
		"architect/logs",
		"researcher/inbox",
		"researcher/logs",
		"concierge/inbox",
	}
	for _, dir := range expectedDirs {
		full := filepath.Join(poolDir, dir)
		if info, err := os.Stat(full); err != nil || !info.IsDir() {
			t.Errorf("expected directory %s to exist", dir)
		}
	}

	// Verify pool.toml content
	data, err := os.ReadFile(filepath.Join(poolDir, "pool.toml"))
	if err != nil {
		t.Fatalf("reading pool.toml: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, `name = "my-project"`) {
		t.Error("pool.toml missing pool name")
	}
	if !strings.Contains(content, `project_dir = "/home/user/my-project"`) {
		t.Error("pool.toml missing project_dir")
	}
}

func TestInitPool_AlreadyExists(t *testing.T) {
	tmp := t.TempDir()
	poolDir := filepath.Join(tmp, ".agent-pool")

	// First init succeeds
	if err := initPool(poolDir, "test", "/tmp"); err != nil {
		t.Fatalf("first initPool: %v", err)
	}

	// Second init fails
	err := initPool(poolDir, "test", "/tmp")
	if err == nil {
		t.Fatal("expected error on second init, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want 'already exists'", err.Error())
	}
}

func TestInitPool_GeneratesValidConfig(t *testing.T) {
	tmp := t.TempDir()
	poolDir := filepath.Join(tmp, ".agent-pool")

	if err := initPool(poolDir, "my-project", "/home/user/my-project"); err != nil {
		t.Fatalf("initPool: %v", err)
	}

	// The generated TOML should be parseable by config.LoadPool
	cfg, err := config.LoadPool(poolDir)
	if err != nil {
		t.Fatalf("LoadPool on init output: %v", err)
	}
	if cfg.Pool.Name != "my-project" {
		t.Errorf("pool name = %q, want %q", cfg.Pool.Name, "my-project")
	}
	if cfg.Architect.Model != "opus" {
		t.Errorf("architect model = %q, want %q", cfg.Architect.Model, "opus")
	}
	if cfg.Defaults.Model != "sonnet" {
		t.Errorf("defaults model = %q, want %q", cfg.Defaults.Model, "sonnet")
	}
}
