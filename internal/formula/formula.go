// Package formula handles TOML workflow template parsing and validation.
//
// Formulas are reusable DAG templates that the architect instantiates to
// bulk-create tasks with correct dependency edges. The daemon's existing
// taskboard + EvaluateDeps handles dispatch — no LLM needed for sequencing.
package formula

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Step is a single node in a formula's dependency graph.
type Step struct {
	ID          string   `toml:"id"`
	Role        string   `toml:"role"` // "concierge", "architect", or expert name
	Title       string   `toml:"title"`
	Description string   `toml:"description"`
	DependsOn   []string `toml:"depends_on"`
}

// Formula is a TOML workflow template with a description and step DAG.
type Formula struct {
	Description string `toml:"description"`
	Steps       []Step `toml:"steps"`
}

// Load reads and validates a single formula TOML file.
func Load(path string) (*Formula, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading formula %s: %w", path, err)
	}

	var f Formula
	if err := toml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parsing formula %s: %w", path, err)
	}

	if err := Validate(&f); err != nil {
		return nil, fmt.Errorf("validating formula %s: %w", path, err)
	}

	return &f, nil
}

// LoadAll scans a directory for *.toml files and returns all valid formulas
// keyed by filename (without the .toml extension).
func LoadAll(dir string) (map[string]*Formula, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading formulas directory %s: %w", dir, err)
	}

	formulas := make(map[string]*Formula)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".toml")
		f, err := Load(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		formulas[name] = f
	}

	return formulas, nil
}

// Validate checks a formula for structural correctness:
//   - At least one step
//   - Non-empty id, role, title on each step
//   - No duplicate step IDs
//   - All depends_on refs point to valid step IDs
//   - No cycles in the dependency graph
func Validate(f *Formula) error {
	if f == nil {
		return fmt.Errorf("formula is nil")
	}
	if len(f.Steps) == 0 {
		return fmt.Errorf("formula has no steps")
	}

	ids := make(map[string]bool, len(f.Steps))
	for _, s := range f.Steps {
		if s.ID == "" {
			return fmt.Errorf("step has empty id")
		}
		if s.ID != filepath.Base(s.ID) || s.ID == "." || s.ID == ".." {
			return fmt.Errorf("step id %q is not filename-safe", s.ID)
		}
		if s.Role == "" {
			return fmt.Errorf("step %q has empty role", s.ID)
		}
		if s.Title == "" {
			return fmt.Errorf("step %q has empty title", s.ID)
		}
		if ids[s.ID] {
			return fmt.Errorf("duplicate step id %q", s.ID)
		}
		ids[s.ID] = true
	}

	// Validate depends_on references
	for _, s := range f.Steps {
		for _, dep := range s.DependsOn {
			if !ids[dep] {
				return fmt.Errorf("step %q depends on unknown step %q", s.ID, dep)
			}
		}
	}

	// Cycle detection via Kahn's algorithm
	if cycle := detectCycle(f.Steps); len(cycle) > 0 {
		return fmt.Errorf("dependency cycle detected involving steps: %v", cycle)
	}

	return nil
}

// detectCycle uses Kahn's algorithm to find cycles in the step DAG.
// Returns the IDs of steps involved in cycles, or nil if acyclic.
func detectCycle(steps []Step) []string {
	inDegree := make(map[string]int, len(steps))
	dependents := make(map[string][]string) // dep → steps that depend on it

	for _, s := range steps {
		if _, ok := inDegree[s.ID]; !ok {
			inDegree[s.ID] = 0
		}
		for _, dep := range s.DependsOn {
			inDegree[s.ID]++
			dependents[dep] = append(dependents[dep], s.ID)
			if _, ok := inDegree[dep]; !ok {
				inDegree[dep] = 0
			}
		}
	}

	var queue []string
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	removed := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		removed++
		for _, depID := range dependents[id] {
			inDegree[depID]--
			if inDegree[depID] == 0 {
				queue = append(queue, depID)
			}
		}
	}

	if removed == len(inDegree) {
		return nil
	}

	var cycleMembers []string
	for id, deg := range inDegree {
		if deg > 0 {
			cycleMembers = append(cycleMembers, id)
		}
	}
	return cycleMembers
}
