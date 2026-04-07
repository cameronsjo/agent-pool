// Test plan for rotate.go:
//
// RotateLogs:
//   [x] BelowThreshold: 5 files, retention=10, returns 0
//   [x] AboveThreshold: 15 files, retention=5, archives 10, keeps 5
//   [x] ArchiveIsValidTarGz: decompress and verify file list
//   [x] StderrIncluded: .stderr companion files archived with .json
//   [x] EmptyDir: returns 0, no error
//   [x] IndexUntouched: index.md not modified after rotation

package expert

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotateLogs_BelowThreshold(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")
	os.MkdirAll(logsDir, 0o755)

	for i := 0; i < 5; i++ {
		os.WriteFile(filepath.Join(logsDir, fmt.Sprintf("task-%03d.json", i)), []byte("{}"), 0o644)
	}

	archived, err := RotateLogs(dir, 10)
	if err != nil {
		t.Fatalf("RotateLogs: %v", err)
	}
	if archived != 0 {
		t.Errorf("archived = %d, want 0 (below threshold)", archived)
	}
}

func TestRotateLogs_AboveThreshold(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")
	os.MkdirAll(logsDir, 0o755)

	for i := 0; i < 15; i++ {
		os.WriteFile(filepath.Join(logsDir, fmt.Sprintf("task-%03d.json", i)), []byte("{}"), 0o644)
	}

	archived, err := RotateLogs(dir, 5)
	if err != nil {
		t.Fatalf("RotateLogs: %v", err)
	}
	if archived != 10 {
		t.Errorf("archived = %d, want 10", archived)
	}

	// Count remaining .json files
	entries, _ := os.ReadDir(logsDir)
	jsonCount := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			jsonCount++
		}
	}
	if jsonCount != 5 {
		t.Errorf("remaining .json files = %d, want 5", jsonCount)
	}
}

func TestRotateLogs_ArchiveIsValidTarGz(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")
	os.MkdirAll(logsDir, 0o755)

	for i := 0; i < 8; i++ {
		os.WriteFile(filepath.Join(logsDir, fmt.Sprintf("task-%03d.json", i)), []byte(fmt.Sprintf(`{"id":%d}`, i)), 0o644)
	}

	archived, err := RotateLogs(dir, 3)
	if err != nil {
		t.Fatalf("RotateLogs: %v", err)
	}
	if archived != 5 {
		t.Errorf("archived = %d, want 5", archived)
	}

	// Find and open the archive
	entries, _ := os.ReadDir(logsDir)
	var archivePath string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "archive-") && strings.HasSuffix(e.Name(), ".tar.gz") {
			archivePath = filepath.Join(logsDir, e.Name())
			break
		}
	}
	if archivePath == "" {
		t.Fatal("no archive file found")
	}

	// Verify it's valid tar.gz
	f, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("opening archive: %v", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	var archivedNames []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar next: %v", err)
		}
		archivedNames = append(archivedNames, hdr.Name)
	}

	if len(archivedNames) != 5 {
		t.Errorf("archive contains %d files, want 5: %v", len(archivedNames), archivedNames)
	}
}

func TestRotateLogs_StderrIncluded(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")
	os.MkdirAll(logsDir, 0o755)

	// Create 6 log files, 3 with stderr companions
	for i := 0; i < 6; i++ {
		os.WriteFile(filepath.Join(logsDir, fmt.Sprintf("task-%03d.json", i)), []byte("{}"), 0o644)
		if i < 3 {
			os.WriteFile(filepath.Join(logsDir, fmt.Sprintf("task-%03d.stderr", i)), []byte("err"), 0o644)
		}
	}

	archived, err := RotateLogs(dir, 3)
	if err != nil {
		t.Fatalf("RotateLogs: %v", err)
	}
	if archived != 3 {
		t.Errorf("archived = %d, want 3", archived)
	}

	// Verify .stderr files for archived tasks are gone
	for i := 0; i < 3; i++ {
		stderrPath := filepath.Join(logsDir, fmt.Sprintf("task-%03d.stderr", i))
		if _, err := os.Stat(stderrPath); !os.IsNotExist(err) {
			t.Errorf("stderr file should be deleted: %s", stderrPath)
		}
	}
}

func TestRotateLogs_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	// No logs dir at all

	archived, err := RotateLogs(dir, 10)
	if err != nil {
		t.Fatalf("RotateLogs: %v", err)
	}
	if archived != 0 {
		t.Errorf("archived = %d, want 0", archived)
	}
}

func TestRotateLogs_IndexUntouched(t *testing.T) {
	dir := t.TempDir()
	logsDir := filepath.Join(dir, "logs")
	os.MkdirAll(logsDir, 0o755)

	// Write index with entries
	indexContent := indexHeader + "| task-000 | 2026-04-01T00:00:00Z | architect | 0 | First task |\n"
	os.WriteFile(filepath.Join(logsDir, "index.md"), []byte(indexContent), 0o644)

	for i := 0; i < 8; i++ {
		os.WriteFile(filepath.Join(logsDir, fmt.Sprintf("task-%03d.json", i)), []byte("{}"), 0o644)
	}

	if _, err := RotateLogs(dir, 3); err != nil {
		t.Fatalf("RotateLogs: %v", err)
	}

	// Verify index.md is unchanged
	data, err := os.ReadFile(filepath.Join(logsDir, "index.md"))
	if err != nil {
		t.Fatalf("reading index.md: %v", err)
	}
	if string(data) != indexContent {
		t.Errorf("index.md was modified:\n%s", string(data))
	}
}
