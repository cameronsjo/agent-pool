// Test Plan for config.go
//
// LoadPool (Classification: CONFIGURATION)
//   [x] Happy: valid pool.toml with all sections
//   [x] Happy: minimal pool.toml (only [pool] + [experts]) — verify all defaults applied
//   [x] Unhappy: missing pool.toml file
//   [x] Unhappy: malformed TOML
//   [x] Unhappy: empty file (valid TOML but no sections)
//   [x] Boundary: poolDir with tilde expansion
//   [x] Happy: multiple experts configured
//   [x] Happy: expert with custom allowed_tools
//
// DefaultsSection.ParseSessionTimeout (Classification: DATA TRANSFORMER)
//   [x] Happy: valid duration "10m" (TestDefaultsSection_ParseSessionTimeout)
//   [x] Happy: valid duration "30s" (TestDefaultsSection_ParseSessionTimeout)
//   [x] Unhappy: invalid duration string (TestDefaultsSection_ParseSessionTimeout)

package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cameronsjo/agent-pool/internal/config"
)

func TestLoadPool_FullConfig(t *testing.T) {
	dir := t.TempDir()

	toml := `
[pool]
name = "full-test"
project_dir = "/home/user/project"

[concierge]
model = "opus"
session_timeout = "30m"

[architect]
model = "opus"
session_timeout = "60m"
approval_mode = "all"
human_inbox = "telegram"

[researcher]
model = "haiku"
session_timeout = "5m"

[shared]
include = ["go-expert", "security-auditor"]

[defaults]
model = "opus"
allowed_tools = ["Read", "Bash"]
session_timeout = "15m"

[curation]
interval_tasks = 5
interval_hours = 24

[experts.auth]
model = "sonnet"
allowed_tools = ["Read", "Write", "Edit"]
`
	writePoolTOML(t, dir, toml)

	cfg, err := config.LoadPool(dir)
	if err != nil {
		t.Fatalf("LoadPool returned error: %v", err)
	}

	// Pool section
	assertEqual(t, "Pool.Name", "full-test", cfg.Pool.Name)
	assertEqual(t, "Pool.ProjectDir", "/home/user/project", cfg.Pool.ProjectDir)

	// Concierge
	assertEqual(t, "Concierge.Model", "opus", cfg.Concierge.Model)
	assertEqual(t, "Concierge.SessionTimeout", "30m", cfg.Concierge.SessionTimeout)

	// Architect
	assertEqual(t, "Architect.Model", "opus", cfg.Architect.Model)
	assertEqual(t, "Architect.SessionTimeout", "60m", cfg.Architect.SessionTimeout)
	assertEqual(t, "Architect.ApprovalMode", "all", cfg.Architect.ApprovalMode)
	assertEqual(t, "Architect.HumanInbox", "telegram", cfg.Architect.HumanInbox)

	// Researcher
	assertEqual(t, "Researcher.Model", "haiku", cfg.Researcher.Model)
	assertEqual(t, "Researcher.SessionTimeout", "5m", cfg.Researcher.SessionTimeout)

	// Shared
	assertSliceEqual(t, "Shared.Include", []string{"go-expert", "security-auditor"}, cfg.Shared.Include)

	// Defaults — explicitly set, not defaulted
	assertEqual(t, "Defaults.Model", "opus", cfg.Defaults.Model)
	assertSliceEqual(t, "Defaults.AllowedTools", []string{"Read", "Bash"}, cfg.Defaults.AllowedTools)
	assertEqual(t, "Defaults.SessionTimeout", "15m", cfg.Defaults.SessionTimeout)

	// Curation
	assertIntEqual(t, "Curation.IntervalTasks", 5, cfg.Curation.IntervalTasks)
	assertIntEqual(t, "Curation.IntervalHours", 24, cfg.Curation.IntervalHours)

	// Experts
	if len(cfg.Experts) != 1 {
		t.Fatalf("expected 1 expert, got %d", len(cfg.Experts))
	}
	auth, ok := cfg.Experts["auth"]
	if !ok {
		t.Fatal("expected expert 'auth' to exist")
	}
	assertEqual(t, "Experts[auth].Model", "sonnet", auth.Model)
	assertSliceEqual(t, "Experts[auth].AllowedTools", []string{"Read", "Write", "Edit"}, auth.AllowedTools)
}

func TestLoadPool_Defaults(t *testing.T) {
	dir := t.TempDir()

	toml := `
[pool]
name = "test"
project_dir = "/tmp/project"

[experts.auth]
`
	writePoolTOML(t, dir, toml)

	cfg, err := config.LoadPool(dir)
	if err != nil {
		t.Fatalf("LoadPool returned error: %v", err)
	}

	assertEqual(t, "Defaults.Model", "sonnet", cfg.Defaults.Model)
	assertEqual(t, "Defaults.SessionTimeout", "10m", cfg.Defaults.SessionTimeout)
	assertSliceEqual(t, "Defaults.AllowedTools",
		[]string{"Read", "Write", "Edit", "Bash", "Grep", "Glob"},
		cfg.Defaults.AllowedTools,
	)
	assertEqual(t, "Architect.ApprovalMode", "decomposition", cfg.Architect.ApprovalMode)
	assertEqual(t, "Architect.HumanInbox", "stdout", cfg.Architect.HumanInbox)
	assertIntEqual(t, "Curation.IntervalTasks", 10, cfg.Curation.IntervalTasks)
	assertIntEqual(t, "Curation.IntervalHours", 168, cfg.Curation.IntervalHours)
}

func TestLoadPool_MissingFile(t *testing.T) {
	dir := t.TempDir()

	_, err := config.LoadPool(dir)
	if err == nil {
		t.Fatal("expected error for missing pool.toml, got nil")
	}
	if !strings.Contains(err.Error(), "reading") {
		t.Errorf("expected error to contain 'reading', got: %v", err)
	}
}

func TestLoadPool_MalformedTOML(t *testing.T) {
	dir := t.TempDir()

	writePoolTOML(t, dir, `[pool
name = broken"syntax
`)

	_, err := config.LoadPool(dir)
	if err == nil {
		t.Fatal("expected error for malformed TOML, got nil")
	}
	if !strings.Contains(err.Error(), "parsing") {
		t.Errorf("expected error to contain 'parsing', got: %v", err)
	}
}

func TestLoadPool_EmptyFile(t *testing.T) {
	dir := t.TempDir()

	writePoolTOML(t, dir, "")

	cfg, err := config.LoadPool(dir)
	if err != nil {
		t.Fatalf("LoadPool returned error: %v", err)
	}

	// All defaults should be applied
	assertEqual(t, "Defaults.Model", "sonnet", cfg.Defaults.Model)
	assertEqual(t, "Defaults.SessionTimeout", "10m", cfg.Defaults.SessionTimeout)
	assertSliceEqual(t, "Defaults.AllowedTools",
		[]string{"Read", "Write", "Edit", "Bash", "Grep", "Glob"},
		cfg.Defaults.AllowedTools,
	)
	assertEqual(t, "Architect.ApprovalMode", "decomposition", cfg.Architect.ApprovalMode)
	assertEqual(t, "Architect.HumanInbox", "stdout", cfg.Architect.HumanInbox)
	assertIntEqual(t, "Curation.IntervalTasks", 10, cfg.Curation.IntervalTasks)
	assertIntEqual(t, "Curation.IntervalHours", 168, cfg.Curation.IntervalHours)

	// Experts map should be nil for an empty file
	if cfg.Experts != nil {
		t.Errorf("expected nil experts map, got %v", cfg.Experts)
	}
}

func TestLoadPool_TildeExpansion(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot determine home directory: %v", err)
	}

	// Create a temp dir inside the home directory for tilde expansion
	dir, err := os.MkdirTemp(home, "agent-pool-test-*")
	if err != nil {
		t.Fatalf("creating temp dir in home: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	writePoolTOML(t, dir, `
[pool]
name = "tilde-test"
project_dir = "/tmp/project"
`)

	// Build a tilde-prefixed path: ~/relative-part
	rel, err := filepath.Rel(home, dir)
	if err != nil {
		t.Fatalf("computing relative path: %v", err)
	}
	tildeDir := "~/" + rel

	cfg, err := config.LoadPool(tildeDir)
	if err != nil {
		t.Fatalf("LoadPool with tilde path returned error: %v", err)
	}
	assertEqual(t, "Pool.Name", "tilde-test", cfg.Pool.Name)
}

func TestLoadPool_MultipleExperts(t *testing.T) {
	dir := t.TempDir()

	toml := `
[pool]
name = "multi"
project_dir = "/tmp/project"

[experts.auth]
model = "opus"
allowed_tools = ["Read"]

[experts.database]
model = "sonnet"
allowed_tools = ["Read", "Bash"]

[experts.frontend]
model = "haiku"
allowed_tools = ["Read", "Write", "Edit", "Glob"]
`
	writePoolTOML(t, dir, toml)

	cfg, err := config.LoadPool(dir)
	if err != nil {
		t.Fatalf("LoadPool returned error: %v", err)
	}

	if len(cfg.Experts) != 3 {
		t.Fatalf("expected 3 experts, got %d", len(cfg.Experts))
	}

	for _, name := range []string{"auth", "database", "frontend"} {
		if _, ok := cfg.Experts[name]; !ok {
			t.Errorf("expected expert %q to exist", name)
		}
	}

	assertEqual(t, "Experts[auth].Model", "opus", cfg.Experts["auth"].Model)
	assertEqual(t, "Experts[database].Model", "sonnet", cfg.Experts["database"].Model)
	assertEqual(t, "Experts[frontend].Model", "haiku", cfg.Experts["frontend"].Model)
}

func TestLoadPool_ExpertCustomTools(t *testing.T) {
	dir := t.TempDir()

	toml := `
[pool]
name = "custom-tools"
project_dir = "/tmp/project"

[experts.infra]
model = "sonnet"
allowed_tools = ["Bash", "Read", "Grep", "Glob", "Write"]
`
	writePoolTOML(t, dir, toml)

	cfg, err := config.LoadPool(dir)
	if err != nil {
		t.Fatalf("LoadPool returned error: %v", err)
	}

	infra, ok := cfg.Experts["infra"]
	if !ok {
		t.Fatal("expected expert 'infra' to exist")
	}

	expected := []string{"Bash", "Read", "Grep", "Glob", "Write"}
	assertSliceEqual(t, "Experts[infra].AllowedTools", expected, infra.AllowedTools)
}

// writePoolTOML writes content to pool.toml in the given directory.
func writePoolTOML(t *testing.T, dir string, content string) {
	t.Helper()
	err := os.WriteFile(filepath.Join(dir, "pool.toml"), []byte(content), 0o644)
	if err != nil {
		t.Fatalf("writing pool.toml: %v", err)
	}
}

// assertEqual compares two strings and fails with a descriptive message.
func assertEqual(t *testing.T, field string, expected string, actual string) {
	t.Helper()
	if expected != actual {
		t.Errorf("%s: expected %q, got %q", field, expected, actual)
	}
}

// assertIntEqual compares two ints and fails with a descriptive message.
func assertIntEqual(t *testing.T, field string, expected int, actual int) {
	t.Helper()
	if expected != actual {
		t.Errorf("%s: expected %d, got %d", field, expected, actual)
	}
}

// assertSliceEqual compares two string slices and fails with a descriptive message.
func assertSliceEqual(t *testing.T, field string, expected []string, actual []string) {
	t.Helper()
	if len(expected) != len(actual) {
		t.Errorf("%s: expected %v (len %d), got %v (len %d)", field, expected, len(expected), actual, len(actual))
		return
	}
	for i := range expected {
		if expected[i] != actual[i] {
			t.Errorf("%s[%d]: expected %q, got %q", field, i, expected[i], actual[i])
		}
	}
}

func TestDefaultsSection_ParseSessionTimeout(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"10 minutes", "10m", 10 * time.Minute, false},
		{"30 seconds", "30s", 30 * time.Second, false},
		{"1 hour", "1h", time.Hour, false},
		{"invalid", "invalid", 0, true},
		{"empty", "", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := config.DefaultsSection{SessionTimeout: tc.input}
			got, err := d.ParseSessionTimeout()
			if tc.wantErr {
				if err == nil {
					t.Errorf("ParseSessionTimeout(%q) expected error", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSessionTimeout(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("ParseSessionTimeout(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}
