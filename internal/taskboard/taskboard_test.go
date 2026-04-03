// Test plan for internal/taskboard
//
// Coverage matrix:
//
// Board CRUD:
//   - Add + Get: add task, retrieve by ID, verify fields
//   - Add duplicate: same ID twice returns error
//   - Add empty ID: returns error
//   - Add defaults CreatedAt: auto-set when zero
//   - Get missing: returns false
//   - Update: mutate status via callback, verify change
//   - Update missing: returns error
//   - TasksByStatus: filter by pending/active/completed
//   - HasActive: true when expert has active task, false otherwise
//
// Persistence:
//   - Save + Load roundtrip: marshal, unmarshal, verify fields
//   - Load missing file: returns empty board, no error
//   - Save atomic: temp file cleaned up after rename
//
// DAG Evaluator:
//   - Simple dep chain: A depends on B, B completes, A becomes ready
//   - Multi-dep: A depends on B and C, both must complete
//   - Partial completion: A depends on B and C, only B completes, A stays blocked
//   - Cycle detection: A→B→C→A detected
//   - Self-cycle: A depends on A
//   - No cycle: linear chain validates clean
//   - Failed dep propagation: B fails, A (depends on B) fails transitively
//   - Cancelled dep propagation: B cancelled, A transitions to failed
//   - Diamond dependency: A→{B,C}→D lifecycle
//   - ValidateAdd: rejects cycle-creating additions
//
// Handoff:
//   - RecordHandoff: increments count
//   - RecordHandoff escalation: needs_attention after 2
//   - RecordHandoff missing: returns error
package taskboard

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBoard_AddAndGet(t *testing.T) {
	b := New()
	now := time.Now().UTC()
	task := &Task{
		ID:        "task-001",
		Status:    StatusPending,
		Expert:    "auth",
		From:      "architect",
		Type:      "task",
		Priority:  "normal",
		CreatedAt: now,
	}

	if err := b.Add(task); err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, ok := b.Get("task-001")
	if !ok {
		t.Fatal("Get returned false for existing task")
	}
	if got.Expert != "auth" {
		t.Errorf("Expert = %q, want %q", got.Expert, "auth")
	}
	if got.CreatedAt != now {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, now)
	}
}

func TestBoard_AddDuplicate(t *testing.T) {
	b := New()
	task := &Task{ID: "task-001", Status: StatusPending}

	if err := b.Add(task); err != nil {
		t.Fatalf("first Add: %v", err)
	}

	err := b.Add(&Task{ID: "task-001", Status: StatusPending})
	if err == nil {
		t.Fatal("expected error for duplicate ID, got nil")
	}
}

func TestBoard_AddEmptyID(t *testing.T) {
	b := New()
	err := b.Add(&Task{Status: StatusPending})
	if err == nil {
		t.Fatal("expected error for empty ID, got nil")
	}
}

func TestBoard_AddDefaultsCreatedAt(t *testing.T) {
	b := New()
	task := &Task{ID: "task-001", Status: StatusPending}

	before := time.Now().UTC()
	if err := b.Add(task); err != nil {
		t.Fatalf("Add: %v", err)
	}
	after := time.Now().UTC()

	if task.CreatedAt.Before(before) || task.CreatedAt.After(after) {
		t.Errorf("CreatedAt %v not between %v and %v", task.CreatedAt, before, after)
	}
}

func TestBoard_GetMissing(t *testing.T) {
	b := New()
	_, ok := b.Get("nonexistent")
	if ok {
		t.Fatal("Get returned true for missing task")
	}
}

func TestBoard_Update(t *testing.T) {
	b := New()
	b.Add(&Task{ID: "task-001", Status: StatusPending})

	err := b.Update("task-001", func(t *Task) {
		t.Status = StatusActive
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _ := b.Get("task-001")
	if got.Status != StatusActive {
		t.Errorf("Status = %q, want %q", got.Status, StatusActive)
	}
}

func TestBoard_UpdateMissing(t *testing.T) {
	b := New()
	err := b.Update("nonexistent", func(t *Task) {})
	if err == nil {
		t.Fatal("expected error for missing task, got nil")
	}
}

func TestBoard_TasksByStatus(t *testing.T) {
	b := New()
	b.Add(&Task{ID: "t1", Status: StatusPending})
	b.Add(&Task{ID: "t2", Status: StatusActive})
	b.Add(&Task{ID: "t3", Status: StatusPending})
	b.Add(&Task{ID: "t4", Status: StatusCompleted})

	pending := b.TasksByStatus(StatusPending)
	if len(pending) != 2 {
		t.Errorf("TasksByStatus(pending) = %d tasks, want 2", len(pending))
	}

	active := b.TasksByStatus(StatusActive)
	if len(active) != 1 {
		t.Errorf("TasksByStatus(active) = %d tasks, want 1", len(active))
	}

	failed := b.TasksByStatus(StatusFailed)
	if len(failed) != 0 {
		t.Errorf("TasksByStatus(failed) = %d tasks, want 0", len(failed))
	}
}

func TestBoard_HasActive(t *testing.T) {
	b := New()
	b.Add(&Task{ID: "t1", Status: StatusActive, Expert: "auth"})
	b.Add(&Task{ID: "t2", Status: StatusPending, Expert: "frontend"})

	if !b.HasActive("auth") {
		t.Error("HasActive(auth) = false, want true")
	}
	if b.HasActive("frontend") {
		t.Error("HasActive(frontend) = true, want false")
	}
	if b.HasActive("nonexistent") {
		t.Error("HasActive(nonexistent) = true, want false")
	}
}

func TestBoard_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "taskboard.json")

	b := New()
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	b.Add(&Task{
		ID:        "task-001",
		Status:    StatusPending,
		Expert:    "auth",
		DependsOn: []string{"task-000"},
		From:      "architect",
		Type:      "task",
		Priority:  "high",
		CreatedAt: now,
	})

	if err := b.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got, ok := loaded.Get("task-001")
	if !ok {
		t.Fatal("loaded board missing task-001")
	}
	if got.Expert != "auth" {
		t.Errorf("Expert = %q, want %q", got.Expert, "auth")
	}
	if got.Priority != "high" {
		t.Errorf("Priority = %q, want %q", got.Priority, "high")
	}
	if len(got.DependsOn) != 1 || got.DependsOn[0] != "task-000" {
		t.Errorf("DependsOn = %v, want [task-000]", got.DependsOn)
	}
	if loaded.Version != 1 {
		t.Errorf("Version = %d, want 1", loaded.Version)
	}
}

func TestBoard_LoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent.json")

	b, err := Load(path)
	if err != nil {
		t.Fatalf("Load missing file: %v", err)
	}
	if b == nil {
		t.Fatal("Load returned nil board")
	}
	if len(b.Tasks) != 0 {
		t.Errorf("Tasks = %d, want 0", len(b.Tasks))
	}
}

func TestBoard_SaveAtomicCleanup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "taskboard.json")

	b := New()
	b.Add(&Task{ID: "t1", Status: StatusPending})
	if err := b.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify no temp files remain
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "taskboard.json" {
			t.Errorf("unexpected file after save: %s", e.Name())
		}
	}
}

// --- DAG Evaluator Tests ---

func TestDAG_SimpleDepChain(t *testing.T) {
	b := New()
	b.Add(&Task{ID: "B", Status: StatusCompleted})
	b.Add(&Task{ID: "A", Status: StatusBlocked, DependsOn: []string{"B"}})

	ready := b.EvaluateDeps()

	a, _ := b.Get("A")
	if a.Status != StatusPending {
		t.Errorf("A.Status = %q, want %q", a.Status, StatusPending)
	}
	if len(ready) != 1 || ready[0] != "A" {
		t.Errorf("ready = %v, want [A]", ready)
	}
}

func TestDAG_MultipleDeps(t *testing.T) {
	b := New()
	b.Add(&Task{ID: "B", Status: StatusCompleted})
	b.Add(&Task{ID: "C", Status: StatusCompleted})
	b.Add(&Task{ID: "A", Status: StatusBlocked, DependsOn: []string{"B", "C"}})

	ready := b.EvaluateDeps()

	a, _ := b.Get("A")
	if a.Status != StatusPending {
		t.Errorf("A.Status = %q, want %q", a.Status, StatusPending)
	}
	if len(ready) != 1 || ready[0] != "A" {
		t.Errorf("ready = %v, want [A]", ready)
	}
}

func TestDAG_PartialCompletion(t *testing.T) {
	b := New()
	b.Add(&Task{ID: "B", Status: StatusCompleted})
	b.Add(&Task{ID: "C", Status: StatusActive})
	b.Add(&Task{ID: "A", Status: StatusBlocked, DependsOn: []string{"B", "C"}})

	ready := b.EvaluateDeps()

	a, _ := b.Get("A")
	if a.Status != StatusBlocked {
		t.Errorf("A.Status = %q, want %q", a.Status, StatusBlocked)
	}
	if len(ready) != 0 {
		t.Errorf("ready = %v, want empty", ready)
	}
}

func TestDAG_CycleDetection(t *testing.T) {
	b := New()
	b.Add(&Task{ID: "A", Status: StatusBlocked, DependsOn: []string{"C"}})
	b.Add(&Task{ID: "B", Status: StatusBlocked, DependsOn: []string{"A"}})
	b.Add(&Task{ID: "C", Status: StatusBlocked, DependsOn: []string{"B"}})

	cycles := b.DetectCycles()
	if cycles == nil {
		t.Fatal("expected cycle, got nil")
	}
	if len(cycles[0]) != 3 {
		t.Errorf("cycle members = %d, want 3", len(cycles[0]))
	}
}

func TestDAG_SelfCycle(t *testing.T) {
	b := New()
	b.Add(&Task{ID: "A", Status: StatusBlocked, DependsOn: []string{"A"}})

	cycles := b.DetectCycles()
	if cycles == nil {
		t.Fatal("expected self-cycle, got nil")
	}
}

func TestDAG_NoCycle(t *testing.T) {
	b := New()
	b.Add(&Task{ID: "A", Status: StatusBlocked, DependsOn: []string{"B"}})
	b.Add(&Task{ID: "B", Status: StatusBlocked, DependsOn: []string{"C"}})
	b.Add(&Task{ID: "C", Status: StatusPending})

	cycles := b.DetectCycles()
	if cycles != nil {
		t.Errorf("expected no cycles, got %v", cycles)
	}
}

func TestDAG_FailedDepPropagation(t *testing.T) {
	b := New()
	b.Add(&Task{ID: "B", Status: StatusFailed})
	b.Add(&Task{ID: "A", Status: StatusBlocked, DependsOn: []string{"B"}})

	b.EvaluateDeps()

	a, _ := b.Get("A")
	if a.Status != StatusFailed {
		t.Errorf("A.Status = %q, want %q", a.Status, StatusFailed)
	}
	if a.CancelNote == "" {
		t.Error("A.CancelNote should be set when dep fails")
	}
}

func TestDAG_CancelledDepPropagation(t *testing.T) {
	b := New()
	b.Add(&Task{ID: "B", Status: StatusCancelled})
	b.Add(&Task{ID: "A", Status: StatusBlocked, DependsOn: []string{"B"}})

	b.EvaluateDeps()

	a, _ := b.Get("A")
	if a.Status != StatusFailed {
		t.Errorf("A.Status = %q, want %q", a.Status, StatusFailed)
	}
}

func TestDAG_TransitiveFailurePropagation(t *testing.T) {
	b := New()
	b.Add(&Task{ID: "C", Status: StatusFailed})
	b.Add(&Task{ID: "B", Status: StatusBlocked, DependsOn: []string{"C"}})
	b.Add(&Task{ID: "A", Status: StatusBlocked, DependsOn: []string{"B"}})

	b.EvaluateDeps()

	bTask, _ := b.Get("B")
	if bTask.Status != StatusFailed {
		t.Errorf("B.Status = %q, want %q", bTask.Status, StatusFailed)
	}
	a, _ := b.Get("A")
	if a.Status != StatusFailed {
		t.Errorf("A.Status = %q, want %q (transitive propagation)", a.Status, StatusFailed)
	}
}

func TestDAG_DiamondDependency(t *testing.T) {
	// D → {B, C} → A
	b := New()
	b.Add(&Task{ID: "D", Status: StatusCompleted})
	b.Add(&Task{ID: "B", Status: StatusBlocked, DependsOn: []string{"D"}})
	b.Add(&Task{ID: "C", Status: StatusBlocked, DependsOn: []string{"D"}})
	b.Add(&Task{ID: "A", Status: StatusBlocked, DependsOn: []string{"B", "C"}})

	// D completed → B and C become ready
	ready := b.EvaluateDeps()
	if len(ready) != 2 {
		t.Fatalf("after D completes: ready = %v, want 2 tasks", ready)
	}

	// Simulate B and C completing
	b.Update("B", func(t *Task) { t.Status = StatusCompleted })
	b.Update("C", func(t *Task) { t.Status = StatusCompleted })

	ready = b.EvaluateDeps()
	a, _ := b.Get("A")
	if a.Status != StatusPending {
		t.Errorf("A.Status = %q, want %q", a.Status, StatusPending)
	}
	if len(ready) != 1 || ready[0] != "A" {
		t.Errorf("after B+C complete: ready = %v, want [A]", ready)
	}
}

func TestDAG_ValidateAddRejectsCycle(t *testing.T) {
	b := New()
	b.Add(&Task{ID: "A", Status: StatusBlocked, DependsOn: []string{"B"}})
	b.Add(&Task{ID: "B", Status: StatusPending})

	// Adding C that depends on A and is depended on by B would create a cycle
	// but B doesn't depend on C yet. Let's create a direct cycle:
	err := b.ValidateAdd(&Task{ID: "C", Status: StatusBlocked, DependsOn: []string{"A"}})
	if err != nil {
		t.Errorf("ValidateAdd should succeed for non-cyclic dep: %v", err)
	}

	// Now create an actual cycle: B depends on A, A depends on B
	b2 := New()
	b2.Add(&Task{ID: "A", Status: StatusBlocked, DependsOn: []string{"B"}})
	b2.Add(&Task{ID: "B", Status: StatusBlocked, DependsOn: []string{"C"}})
	err = b2.ValidateAdd(&Task{ID: "C", Status: StatusBlocked, DependsOn: []string{"A"}})
	if err == nil {
		t.Fatal("ValidateAdd should reject cycle-creating task")
	}
}

func TestDAG_ValidateAddRejectsDuplicate(t *testing.T) {
	b := New()
	b.Add(&Task{ID: "A", Status: StatusPending})

	err := b.ValidateAdd(&Task{ID: "A", Status: StatusPending})
	if err == nil {
		t.Fatal("ValidateAdd should reject duplicate ID")
	}
}

func TestDAG_TerminalTasksIgnoredInCycleDetection(t *testing.T) {
	b := New()
	// A depends on B, but B is completed — no cycle possible
	b.Add(&Task{ID: "B", Status: StatusCompleted, DependsOn: []string{"A"}})
	b.Add(&Task{ID: "A", Status: StatusBlocked, DependsOn: []string{"B"}})

	cycles := b.DetectCycles()
	if cycles != nil {
		t.Errorf("completed tasks should not participate in cycles, got %v", cycles)
	}
}

// --- Handoff Tests ---

func TestBoard_RecordHandoff(t *testing.T) {
	b := New()
	b.Add(&Task{ID: "t1", Status: StatusActive, Expert: "auth"})

	if err := b.RecordHandoff("t1"); err != nil {
		t.Fatalf("RecordHandoff: %v", err)
	}

	task, _ := b.Get("t1")
	if task.HandoffCount != 1 {
		t.Errorf("HandoffCount = %d, want 1", task.HandoffCount)
	}
	if task.NeedsAttention {
		t.Error("NeedsAttention should be false after 1 handoff")
	}
}

func TestBoard_RecordHandoffEscalation(t *testing.T) {
	b := New()
	b.Add(&Task{ID: "t1", Status: StatusActive, Expert: "auth"})

	b.RecordHandoff("t1")
	b.RecordHandoff("t1")

	task, _ := b.Get("t1")
	if task.HandoffCount != 2 {
		t.Errorf("HandoffCount = %d, want 2", task.HandoffCount)
	}
	if !task.NeedsAttention {
		t.Error("NeedsAttention should be true after 2 handoffs")
	}
}

func TestBoard_RecordHandoffMissing(t *testing.T) {
	b := New()
	err := b.RecordHandoff("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing task, got nil")
	}
}
