// Test coverage matrix for formula package:
//
// Load:
//   - [x] Valid formula file parses and validates
//   - [x] Missing file returns error
//   - [x] Invalid TOML returns parse error
//   - [x] Valid TOML but invalid formula returns validation error
//
// LoadAll:
//   - [x] Empty directory returns empty map
//   - [x] Missing directory returns nil (not error)
//   - [x] Multiple valid formulas keyed by name
//   - [x] Non-TOML files ignored
//   - [x] Invalid formula in dir returns error
//
// Validate:
//   - [x] Happy path — linear chain
//   - [x] Happy path — diamond DAG
//   - [x] Empty steps
//   - [x] Empty step ID
//   - [x] Empty step role
//   - [x] Empty step title
//   - [x] Duplicate step IDs
//   - [x] depends_on references unknown step
//   - [x] Simple cycle (A→B→A)
//   - [x] Self-cycle (A→A)
//   - [x] Three-node cycle
package formula

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_Valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.toml")
	os.WriteFile(path, []byte(`
description = "test formula"

[[steps]]
id = "first"
role = "concierge"
title = "First step"
description = "Do the first thing"

[[steps]]
id = "second"
role = "architect"
title = "Second step"
description = "Do the second thing"
depends_on = ["first"]
`), 0o644)

	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if f.Description != "test formula" {
		t.Errorf("Description = %q, want %q", f.Description, "test formula")
	}
	if len(f.Steps) != 2 {
		t.Fatalf("len(Steps) = %d, want 2", len(f.Steps))
	}
	if f.Steps[1].DependsOn[0] != "first" {
		t.Errorf("Steps[1].DependsOn = %v, want [first]", f.Steps[1].DependsOn)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/formula.toml")
	if err == nil {
		t.Fatal("Load() expected error for missing file")
	}
}

func TestLoad_InvalidTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	os.WriteFile(path, []byte(`this is not valid toml [[[`), 0o644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected error for invalid TOML")
	}
	if !strings.Contains(err.Error(), "parsing") {
		t.Errorf("error should mention parsing: %v", err)
	}
}

func TestLoad_ValidTOMLInvalidFormula(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.toml")
	os.WriteFile(path, []byte(`description = "no steps"`), 0o644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() expected error for formula with no steps")
	}
	if !strings.Contains(err.Error(), "no steps") {
		t.Errorf("error should mention no steps: %v", err)
	}
}

func TestLoadAll_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	formulas, err := LoadAll(dir)
	if err != nil {
		t.Fatalf("LoadAll() error: %v", err)
	}
	if len(formulas) != 0 {
		t.Errorf("len(formulas) = %d, want 0", len(formulas))
	}
}

func TestLoadAll_MissingDir(t *testing.T) {
	formulas, err := LoadAll("/nonexistent/dir")
	if err != nil {
		t.Fatalf("LoadAll() error: %v", err)
	}
	if formulas != nil {
		t.Errorf("formulas = %v, want nil", formulas)
	}
}

func TestLoadAll_MultipleFormulas(t *testing.T) {
	dir := t.TempDir()

	formulaTOML := `
description = "test"
[[steps]]
id = "a"
role = "concierge"
title = "Step A"
`

	os.WriteFile(filepath.Join(dir, "alpha.toml"), []byte(formulaTOML), 0o644)
	os.WriteFile(filepath.Join(dir, "beta.toml"), []byte(formulaTOML), 0o644)
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("not a formula"), 0o644)

	formulas, err := LoadAll(dir)
	if err != nil {
		t.Fatalf("LoadAll() error: %v", err)
	}
	if len(formulas) != 2 {
		t.Fatalf("len(formulas) = %d, want 2", len(formulas))
	}
	if formulas["alpha"] == nil || formulas["beta"] == nil {
		t.Errorf("expected formulas keyed by alpha and beta, got keys: %v", mapKeys(formulas))
	}
}

func TestLoadAll_InvalidFormulaReturnsError(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "bad.toml"), []byte(`description = "no steps"`), 0o644)

	_, err := LoadAll(dir)
	if err == nil {
		t.Fatal("LoadAll() expected error for invalid formula")
	}
}

func TestValidate_LinearChain(t *testing.T) {
	f := &Formula{
		Steps: []Step{
			{ID: "a", Role: "concierge", Title: "A"},
			{ID: "b", Role: "architect", Title: "B", DependsOn: []string{"a"}},
			{ID: "c", Role: "experts", Title: "C", DependsOn: []string{"b"}},
		},
	}
	if err := Validate(f); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
}

func TestValidate_DiamondDAG(t *testing.T) {
	f := &Formula{
		Steps: []Step{
			{ID: "root", Role: "concierge", Title: "Root"},
			{ID: "left", Role: "architect", Title: "Left", DependsOn: []string{"root"}},
			{ID: "right", Role: "architect", Title: "Right", DependsOn: []string{"root"}},
			{ID: "join", Role: "concierge", Title: "Join", DependsOn: []string{"left", "right"}},
		},
	}
	if err := Validate(f); err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
}

func TestValidate_EmptySteps(t *testing.T) {
	f := &Formula{Steps: nil}
	err := Validate(f)
	if err == nil || !strings.Contains(err.Error(), "no steps") {
		t.Fatalf("expected 'no steps' error, got: %v", err)
	}
}

func TestValidate_EmptyStepID(t *testing.T) {
	f := &Formula{Steps: []Step{{Role: "x", Title: "y"}}}
	err := Validate(f)
	if err == nil || !strings.Contains(err.Error(), "empty id") {
		t.Fatalf("expected 'empty id' error, got: %v", err)
	}
}

func TestValidate_EmptyStepRole(t *testing.T) {
	f := &Formula{Steps: []Step{{ID: "a", Title: "y"}}}
	err := Validate(f)
	if err == nil || !strings.Contains(err.Error(), "empty role") {
		t.Fatalf("expected 'empty role' error, got: %v", err)
	}
}

func TestValidate_EmptyStepTitle(t *testing.T) {
	f := &Formula{Steps: []Step{{ID: "a", Role: "x"}}}
	err := Validate(f)
	if err == nil || !strings.Contains(err.Error(), "empty title") {
		t.Fatalf("expected 'empty title' error, got: %v", err)
	}
}

func TestValidate_DuplicateIDs(t *testing.T) {
	f := &Formula{Steps: []Step{
		{ID: "a", Role: "x", Title: "y"},
		{ID: "a", Role: "x", Title: "z"},
	}}
	err := Validate(f)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected 'duplicate' error, got: %v", err)
	}
}

func TestValidate_UnknownDependency(t *testing.T) {
	f := &Formula{Steps: []Step{
		{ID: "a", Role: "x", Title: "y", DependsOn: []string{"nonexistent"}},
	}}
	err := Validate(f)
	if err == nil || !strings.Contains(err.Error(), "unknown step") {
		t.Fatalf("expected 'unknown step' error, got: %v", err)
	}
}

func TestValidate_SimpleCycle(t *testing.T) {
	f := &Formula{Steps: []Step{
		{ID: "a", Role: "x", Title: "y", DependsOn: []string{"b"}},
		{ID: "b", Role: "x", Title: "z", DependsOn: []string{"a"}},
	}}
	err := Validate(f)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected 'cycle' error, got: %v", err)
	}
}

func TestValidate_SelfCycle(t *testing.T) {
	f := &Formula{Steps: []Step{
		{ID: "a", Role: "x", Title: "y", DependsOn: []string{"a"}},
	}}
	err := Validate(f)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected 'cycle' error, got: %v", err)
	}
}

func TestValidate_ThreeNodeCycle(t *testing.T) {
	f := &Formula{Steps: []Step{
		{ID: "a", Role: "x", Title: "A", DependsOn: []string{"c"}},
		{ID: "b", Role: "x", Title: "B", DependsOn: []string{"a"}},
		{ID: "c", Role: "x", Title: "C", DependsOn: []string{"b"}},
	}}
	err := Validate(f)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected 'cycle' error, got: %v", err)
	}
}

func mapKeys(m map[string]*Formula) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
