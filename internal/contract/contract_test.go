// Contract package coverage matrix:
//
// Parse (Classification: PURE LOGIC)
//   [x] Happy: valid contract round-trips with Compose (TestParse_RoundTrip)
//   [x] Error: missing id (TestParse_MissingID)
//   [x] Error: wrong type field (TestParse_WrongType)
//   [x] Error: fewer than 2 between entries (TestParse_TooFewBetween)
//   [x] Error: version < 1 (TestParse_InvalidVersion)
//   [x] Error: path traversal in ID (TestParse_PathTraversalID)
//   [x] Error: no frontmatter (TestParse_NoFrontmatter)
//
// Store (Classification: FILESYSTEM I/O)
//   [x] Happy: Save + Load round-trip (TestStore_SaveLoad)
//   [x] Happy: Amend increments version (TestStore_Amend)
//   [x] Happy: List returns all contracts sorted (TestStore_List)
//   [x] Happy: UpdateIndex creates markdown table (TestStore_UpdateIndex)
//   [x] Error: Save duplicate with different version (TestStore_SaveDuplicateVersion)
//   [x] Error: Load nonexistent (TestStore_LoadNotFound)
//   [x] Error: Amend nonexistent (TestStore_AmendNotFound)
package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var validContract = &Contract{
	ID:        "contract-001",
	Type:      "contract",
	DefinedBy: "architect",
	Between:   []string{"auth", "frontend"},
	Version:   1,
	Timestamp: time.Date(2026, 4, 1, 14, 0, 0, 0, time.UTC),
	Body:      "## Auth ↔ Frontend: Token Exchange\n\nSome spec here.",
}

func TestParse_RoundTrip(t *testing.T) {
	composed, err := Compose(validContract)
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}

	parsed, err := Parse(composed)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if parsed.ID != validContract.ID {
		t.Errorf("ID = %q, want %q", parsed.ID, validContract.ID)
	}
	if parsed.Version != validContract.Version {
		t.Errorf("Version = %d, want %d", parsed.Version, validContract.Version)
	}
	if len(parsed.Between) != len(validContract.Between) {
		t.Errorf("Between length = %d, want %d", len(parsed.Between), len(validContract.Between))
	}
	if parsed.Body != validContract.Body {
		t.Errorf("Body = %q, want %q", parsed.Body, validContract.Body)
	}
}

func TestParse_MissingID(t *testing.T) {
	content := "---\ntype: contract\nbetween: [a, b]\nversion: 1\n---\n"
	_, err := Parse(content)
	if err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestParse_WrongType(t *testing.T) {
	content := "---\nid: c1\ntype: task\ndefined-by: arch\nbetween: [a, b]\nversion: 1\ntimestamp: 2026-04-01T00:00:00Z\n---\n"
	_, err := Parse(content)
	if err == nil {
		t.Fatal("expected error for wrong type")
	}
}

func TestParse_TooFewBetween(t *testing.T) {
	content := "---\nid: c1\ntype: contract\ndefined-by: arch\nbetween: [a]\nversion: 1\ntimestamp: 2026-04-01T00:00:00Z\n---\n"
	_, err := Parse(content)
	if err == nil {
		t.Fatal("expected error for too few between entries")
	}
}

func TestParse_InvalidVersion(t *testing.T) {
	content := "---\nid: c1\ntype: contract\ndefined-by: arch\nbetween: [a, b]\nversion: 0\ntimestamp: 2026-04-01T00:00:00Z\n---\n"
	_, err := Parse(content)
	if err == nil {
		t.Fatal("expected error for version < 1")
	}
}

func TestParse_PathTraversalID(t *testing.T) {
	content := "---\nid: ../escape\ntype: contract\ndefined-by: arch\nbetween: [a, b]\nversion: 1\ntimestamp: 2026-04-01T00:00:00Z\n---\n"
	_, err := Parse(content)
	if err == nil {
		t.Fatal("expected error for path traversal in ID")
	}
}

func TestParse_NoFrontmatter(t *testing.T) {
	_, err := Parse("just some text")
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestStore_SaveLoad(t *testing.T) {
	poolDir := t.TempDir()

	store := NewStore(poolDir)
	if err := store.Save(validContract); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load("contract-001")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.ID != validContract.ID {
		t.Errorf("ID = %q, want %q", loaded.ID, validContract.ID)
	}
	if loaded.Version != validContract.Version {
		t.Errorf("Version = %d, want %d", loaded.Version, validContract.Version)
	}
	if loaded.Body != validContract.Body {
		t.Errorf("Body = %q, want %q", loaded.Body, validContract.Body)
	}
}

func TestStore_Amend(t *testing.T) {
	poolDir := t.TempDir()
	store := NewStore(poolDir)

	if err := store.Save(validContract); err != nil {
		t.Fatalf("Save: %v", err)
	}

	amended, err := store.Amend("contract-001", "## Updated spec\n\nNew content.")
	if err != nil {
		t.Fatalf("Amend: %v", err)
	}

	if amended.Version != 2 {
		t.Errorf("Version = %d, want 2", amended.Version)
	}
	if amended.Body != "## Updated spec\n\nNew content." {
		t.Errorf("Body = %q, want updated content", amended.Body)
	}

	// Verify persisted
	loaded, err := store.Load("contract-001")
	if err != nil {
		t.Fatalf("Load after amend: %v", err)
	}
	if loaded.Version != 2 {
		t.Errorf("Persisted version = %d, want 2", loaded.Version)
	}
}

func TestStore_List(t *testing.T) {
	poolDir := t.TempDir()
	store := NewStore(poolDir)

	c1 := &Contract{
		ID: "aaa-first", Type: "contract", DefinedBy: "architect",
		Between: []string{"a", "b"}, Version: 1,
		Timestamp: time.Now().UTC(),
	}
	c2 := &Contract{
		ID: "zzz-second", Type: "contract", DefinedBy: "architect",
		Between: []string{"c", "d"}, Version: 1,
		Timestamp: time.Now().UTC(),
	}

	if err := store.Save(c2); err != nil { // save out of order
		t.Fatalf("Save c2: %v", err)
	}
	if err := store.Save(c1); err != nil {
		t.Fatalf("Save c1: %v", err)
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("List length = %d, want 2", len(list))
	}
	if list[0].ID != "aaa-first" {
		t.Errorf("first contract = %q, want aaa-first", list[0].ID)
	}
	if list[1].ID != "zzz-second" {
		t.Errorf("second contract = %q, want zzz-second", list[1].ID)
	}
}

func TestStore_UpdateIndex(t *testing.T) {
	poolDir := t.TempDir()
	store := NewStore(poolDir)

	if err := store.Save(validContract); err != nil {
		t.Fatalf("Save: %v", err)
	}

	indexPath := filepath.Join(poolDir, "contracts", "index.md")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("reading index: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "contract-001") {
		t.Error("index does not contain contract ID")
	}
	if !strings.Contains(content, "auth, frontend") {
		t.Error("index does not contain between parties")
	}
}

func TestStore_SaveDuplicateVersion(t *testing.T) {
	poolDir := t.TempDir()
	store := NewStore(poolDir)

	if err := store.Save(validContract); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Try to save same ID with different version
	dup := *validContract
	dup.Version = 5
	err := store.Save(&dup)
	if err == nil {
		t.Fatal("expected error saving duplicate with different version")
	}
}

func TestStore_LoadNotFound(t *testing.T) {
	poolDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(poolDir, "contracts"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	store := NewStore(poolDir)

	_, err := store.Load("nonexistent")
	if err == nil {
		t.Fatal("expected error loading nonexistent contract")
	}
}

func TestStore_AmendNotFound(t *testing.T) {
	poolDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(poolDir, "contracts"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	store := NewStore(poolDir)

	_, err := store.Amend("nonexistent", "new body")
	if err == nil {
		t.Fatal("expected error amending nonexistent contract")
	}
}

// --- Fuzz harnesses ---

func FuzzParse(f *testing.F) {
	f.Add("---\nid: c1\ntype: contract\ndefined-by: arch\nbetween: [a, b]\nversion: 1\ntimestamp: 2026-04-01T00:00:00Z\n---\n\nbody\n")
	f.Add("---\nid: x\ntype: contract\nbetween: []\nversion: 0\n---\n")
	f.Add("")
	f.Add("no frontmatter")
	f.Add("---\n---\n")

	f.Fuzz(func(t *testing.T, input string) {
		// Must not panic for any input
		_, _ = Parse(input)
	})
}

func FuzzComposeParseRoundTrip(f *testing.F) {
	f.Add("contract-001", "architect", "auth", "frontend", "body content")
	f.Add("c-1", "arch", "a", "b", "## Spec\n\nDetails.")
	f.Add("simple", "def", "x", "y", "")

	f.Fuzz(func(t *testing.T, id, definedBy, party1, party2, body string) {
		// Skip inputs that would fail validation
		if id == "" || id != filepath.Base(id) || id == "." || id == ".." {
			return
		}
		if party1 == "" || party2 == "" {
			return
		}

		c := &Contract{
			ID:        id,
			Type:      "contract",
			DefinedBy: definedBy,
			Between:   []string{party1, party2},
			Version:   1,
			Timestamp: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Body:      body,
		}

		composed, err := Compose(c)
		if err != nil {
			return // composition can fail for some inputs (e.g., YAML special chars)
		}

		parsed, err := Parse(composed)
		if err != nil {
			t.Fatalf("Parse failed after successful Compose: %v\ncomposed:\n%s", err, composed)
		}

		if parsed.ID != c.ID {
			t.Errorf("ID round-trip: got %q, want %q", parsed.ID, c.ID)
		}
		if parsed.Version != c.Version {
			t.Errorf("Version round-trip: got %d, want %d", parsed.Version, c.Version)
		}
	})
}

// --- Additional gap-filling tests ---

func TestStore_ListSkipsMalformedFiles(t *testing.T) {
	poolDir := t.TempDir()
	store := NewStore(poolDir)

	// Save a valid contract
	if err := store.Save(validContract); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Write a malformed contract file
	malformedPath := filepath.Join(poolDir, "contracts", "bad-contract.md")
	if err := os.WriteFile(malformedPath, []byte("not valid frontmatter"), 0o644); err != nil {
		t.Fatalf("writing malformed: %v", err)
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	// Should return only the valid contract, skipping the malformed one
	if len(list) != 1 {
		t.Errorf("List length = %d, want 1 (malformed should be skipped)", len(list))
	}
	if len(list) > 0 && list[0].ID != "contract-001" {
		t.Errorf("first contract = %q, want contract-001", list[0].ID)
	}
}
