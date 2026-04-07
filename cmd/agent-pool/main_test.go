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
//
// addExpert:
//   - Adds expert to pool.toml + creates dirs
//   - Duplicate name → error
//   - Builtin role name → error
//   - Custom model → written to TOML
//   - No model → section without model line (uses defaults)

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

func TestAddExpert_CreatesExpert(t *testing.T) {
	tmp := t.TempDir()
	poolDir := filepath.Join(tmp, ".agent-pool")
	initPool(poolDir, "test", tmp)

	if err := addExpert(poolDir, "backend", ""); err != nil {
		t.Fatalf("addExpert: %v", err)
	}

	// Verify directories created
	for _, sub := range []string{"inbox", "logs"} {
		dir := filepath.Join(poolDir, "experts", "backend", sub)
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("expected directory experts/backend/%s", sub)
		}
	}

	// Verify config is valid and includes the expert
	cfg, err := config.LoadPool(poolDir)
	if err != nil {
		t.Fatalf("LoadPool after add: %v", err)
	}
	if _, ok := cfg.Experts["backend"]; !ok {
		t.Error("expert 'backend' not found in loaded config")
	}
}

func TestAddExpert_WithModel(t *testing.T) {
	tmp := t.TempDir()
	poolDir := filepath.Join(tmp, ".agent-pool")
	initPool(poolDir, "test", tmp)

	if err := addExpert(poolDir, "frontend", "opus"); err != nil {
		t.Fatalf("addExpert: %v", err)
	}

	cfg, err := config.LoadPool(poolDir)
	if err != nil {
		t.Fatalf("LoadPool: %v", err)
	}
	if cfg.Experts["frontend"].Model != "opus" {
		t.Errorf("model = %q, want %q", cfg.Experts["frontend"].Model, "opus")
	}
}

func TestAddExpert_Duplicate(t *testing.T) {
	tmp := t.TempDir()
	poolDir := filepath.Join(tmp, ".agent-pool")
	initPool(poolDir, "test", tmp)

	addExpert(poolDir, "backend", "")
	err := addExpert(poolDir, "backend", "")
	if err == nil {
		t.Fatal("expected error for duplicate expert")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want 'already exists'", err.Error())
	}
}

func TestAddExpert_InvalidName(t *testing.T) {
	tmp := t.TempDir()
	poolDir := filepath.Join(tmp, ".agent-pool")
	initPool(poolDir, "test", tmp)

	for _, name := range []string{"has space", "has.dot", "has/slash", `has"quote`} {
		err := addExpert(poolDir, name, "")
		if err == nil {
			t.Errorf("expected error for name %q, got nil", name)
		}
	}
}

func TestAddExpert_BuiltinRole(t *testing.T) {
	tmp := t.TempDir()
	poolDir := filepath.Join(tmp, ".agent-pool")
	initPool(poolDir, "test", tmp)

	err := addExpert(poolDir, "architect", "")
	if err == nil {
		t.Fatal("expected error for builtin role name")
	}
	if !strings.Contains(err.Error(), "built-in role") {
		t.Errorf("error = %q, want 'built-in role'", err.Error())
	}
}
