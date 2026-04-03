// Package taskboard manages daemon-owned task status tracking.
//
// The Board is an in-memory data structure backed by taskboard.json.
// It has no internal locking — the daemon's sync.Mutex serializes all access.
// DAG evaluation and cycle detection are pure functions on the board state.
package taskboard

import (
	"fmt"
	"time"
)

// Status represents a task's lifecycle state.
type Status string

const (
	StatusPending   Status = "pending"
	StatusBlocked   Status = "blocked"
	StatusActive    Status = "active"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Task tracks a single work item through its lifecycle.
type Task struct {
	ID             string     `json:"id"`
	Status         Status     `json:"status"`
	Expert         string     `json:"expert"`
	PID            int        `json:"pid,omitempty"`
	DependsOn      []string   `json:"depends_on,omitempty"`
	From           string     `json:"from"`
	Type           string     `json:"type"`
	Priority       string     `json:"priority"`
	CreatedAt      time.Time  `json:"created_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	HandoffCount   int        `json:"handoff_count"`
	NeedsAttention bool       `json:"needs_attention,omitempty"`
	CancelNote     string     `json:"cancel_note,omitempty"`
	ExitCode       *int       `json:"exit_code,omitempty"`
}

// Board is the daemon-managed task registry.
type Board struct {
	Version int              `json:"version"`
	Tasks   map[string]*Task `json:"tasks"`
}

// New returns an empty board.
func New() *Board {
	return &Board{
		Version: 1,
		Tasks:   make(map[string]*Task),
	}
}

// Add registers a task. Returns an error if the ID already exists.
func (b *Board) Add(task *Task) error {
	if task.ID == "" {
		return fmt.Errorf("task ID is required")
	}
	if _, exists := b.Tasks[task.ID]; exists {
		return fmt.Errorf("task %q already exists", task.ID)
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now().UTC()
	}
	b.Tasks[task.ID] = task
	return nil
}

// Get returns a task by ID. The bool indicates whether it was found.
func (b *Board) Get(id string) (*Task, bool) {
	t, ok := b.Tasks[id]
	return t, ok
}

// Update applies a mutation function to the task with the given ID.
func (b *Board) Update(id string, fn func(*Task)) error {
	t, ok := b.Tasks[id]
	if !ok {
		return fmt.Errorf("task %q not found", id)
	}
	fn(t)
	return nil
}

// TasksByStatus returns all tasks matching the given status.
func (b *Board) TasksByStatus(status Status) []*Task {
	var result []*Task
	for _, t := range b.Tasks {
		if t.Status == status {
			result = append(result, t)
		}
	}
	return result
}

// HasActive returns true if the expert has any task in active status.
func (b *Board) HasActive(expert string) bool {
	for _, t := range b.Tasks {
		if t.Expert == expert && t.Status == StatusActive {
			return true
		}
	}
	return false
}
