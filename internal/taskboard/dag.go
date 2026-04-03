package taskboard

import "fmt"

// EvaluateDeps recalculates blocked tasks after a status change.
// Tasks whose dependencies are all completed transition to pending.
// Tasks with any failed or cancelled dependency transition to failed.
// Returns the IDs of tasks that became newly ready (blocked → pending).
func (b *Board) EvaluateDeps() []string {
	var ready []string

	for _, t := range b.Tasks {
		if t.Status != StatusBlocked {
			continue
		}

		if b.depsFailed(t) {
			t.Status = StatusFailed
			t.CancelNote = "dependency failed or cancelled"
			continue
		}

		if b.depsCompleted(t) {
			t.Status = StatusPending
			ready = append(ready, t.ID)
		}
	}

	// Propagation: newly failed tasks may unblock (fail) further downstream.
	// Repeat until stable. Bounded by task count — each iteration fails at
	// least one task, so this terminates.
	for {
		changed := false
		for _, t := range b.Tasks {
			if t.Status != StatusBlocked {
				continue
			}
			if b.depsFailed(t) {
				t.Status = StatusFailed
				t.CancelNote = "dependency failed or cancelled"
				changed = true
			}
		}
		if !changed {
			break
		}
	}

	return ready
}

// depsCompleted returns true if all of the task's dependencies are completed.
func (b *Board) depsCompleted(t *Task) bool {
	for _, depID := range t.DependsOn {
		dep, ok := b.Tasks[depID]
		if !ok {
			return false // unknown dependency — not completed
		}
		if dep.Status != StatusCompleted {
			return false
		}
	}
	return true
}

// depsFailed returns true if any of the task's dependencies are failed or cancelled.
func (b *Board) depsFailed(t *Task) bool {
	for _, depID := range t.DependsOn {
		dep, ok := b.Tasks[depID]
		if !ok {
			continue
		}
		if dep.Status == StatusFailed || dep.Status == StatusCancelled {
			return true
		}
	}
	return false
}

// DetectCycles uses Kahn's algorithm to find cycles in the dependency graph.
// Returns nil if there are no cycles. Returns lists of IDs that form cycles.
func (b *Board) DetectCycles() [][]string {
	// Build adjacency and in-degree only for non-terminal tasks.
	// Terminal tasks (completed, failed, cancelled) are treated as resolved
	// and cannot participate in cycles.
	inDegree := make(map[string]int)
	dependents := make(map[string][]string) // dep → tasks that depend on it

	for _, t := range b.Tasks {
		if isTerminal(t.Status) {
			continue
		}
		if _, ok := inDegree[t.ID]; !ok {
			inDegree[t.ID] = 0
		}
		for _, depID := range t.DependsOn {
			dep, ok := b.Tasks[depID]
			if !ok || isTerminal(dep.Status) {
				continue // resolved or unknown deps don't contribute edges
			}
			inDegree[t.ID]++
			dependents[depID] = append(dependents[depID], t.ID)
			if _, ok := inDegree[depID]; !ok {
				inDegree[depID] = 0
			}
		}
	}

	// Kahn's: start with zero in-degree nodes
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

	// Any remaining nodes with non-zero in-degree are in cycles
	if removed == len(inDegree) {
		return nil
	}

	var cycleMembers []string
	for id, deg := range inDegree {
		if deg > 0 {
			cycleMembers = append(cycleMembers, id)
		}
	}

	if len(cycleMembers) > 0 {
		return [][]string{cycleMembers}
	}
	return nil
}

// ValidateAdd checks whether adding a task with the given dependencies would
// create a cycle. Returns an error describing the cycle if one would result.
func (b *Board) ValidateAdd(task *Task) error {
	if _, exists := b.Tasks[task.ID]; exists {
		return fmt.Errorf("task %q already exists", task.ID)
	}

	// Temporarily add to detect cycles
	b.Tasks[task.ID] = task
	cycles := b.DetectCycles()
	delete(b.Tasks, task.ID)

	if len(cycles) > 0 {
		return fmt.Errorf("adding task %q would create a cycle involving %v", task.ID, cycles[0])
	}
	return nil
}

// RecordHandoff increments the handoff count for a task and sets needs_attention
// if the count reaches the escalation threshold (2).
func (b *Board) RecordHandoff(taskID string) error {
	t, ok := b.Tasks[taskID]
	if !ok {
		return fmt.Errorf("task %q not found", taskID)
	}
	t.HandoffCount++
	if t.HandoffCount >= 2 {
		t.NeedsAttention = true
	}
	return nil
}

func isTerminal(s Status) bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCancelled
}
