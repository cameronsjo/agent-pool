package expert

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultLogRetention is the default number of log files to keep before archiving.
const DefaultLogRetention = 50

// RotateLogs archives log files beyond the retention count into a tar.gz bundle.
// Only .json files count toward the threshold. Archived .json and their matching
// .stderr companion files are deleted after successful archival. index.md is
// never modified — it retains all entries for searchability.
//
// Returns the number of files archived, or 0 if below threshold.
func RotateLogs(expertDir string, retention int) (int, error) {
	if retention <= 0 {
		retention = DefaultLogRetention
	}

	logsDir := filepath.Join(expertDir, "logs")
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("reading logs dir: %w", err)
	}

	// Collect .json files and sort by mtime (oldest first)
	type logFile struct {
		name  string
		mtime time.Time
	}
	var jsonFiles []logFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		jsonFiles = append(jsonFiles, logFile{name: e.Name(), mtime: info.ModTime()})
	}

	if len(jsonFiles) <= retention {
		return 0, nil
	}

	// Sort oldest first
	sort.Slice(jsonFiles, func(i, j int) bool {
		return jsonFiles[i].mtime.Before(jsonFiles[j].mtime)
	})

	// Files to archive: everything before the retention cutoff
	toArchive := jsonFiles[:len(jsonFiles)-retention]

	// Create archive
	archiveName := fmt.Sprintf("archive-%s.tar.gz", time.Now().UTC().Format("20060102T150405Z"))
	archivePath := filepath.Join(logsDir, archiveName)

	f, err := os.Create(archivePath)
	if err != nil {
		return 0, fmt.Errorf("creating archive: %w", err)
	}
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()
	tw := tar.NewWriter(gw)
	defer tw.Close()

	var archived int
	var toDelete []string

	for _, lf := range toArchive {
		jsonPath := filepath.Join(logsDir, lf.name)
		if err := addToTar(tw, jsonPath, lf.name); err != nil {
			return archived, fmt.Errorf("adding %s to archive: %w", lf.name, err)
		}
		toDelete = append(toDelete, jsonPath)
		archived++

		// Also archive matching .stderr if it exists
		stderrName := strings.TrimSuffix(lf.name, ".json") + ".stderr"
		stderrPath := filepath.Join(logsDir, stderrName)
		if _, err := os.Stat(stderrPath); err == nil {
			if err := addToTar(tw, stderrPath, stderrName); err != nil {
				return archived, fmt.Errorf("adding %s to archive: %w", stderrName, err)
			}
			toDelete = append(toDelete, stderrPath)
		}
	}

	// Close writers before deleting source files
	tw.Close()
	gw.Close()
	f.Close()

	// Delete archived files
	for _, path := range toDelete {
		os.Remove(path) // best-effort; files are safely in the archive
	}

	return archived, nil
}

// addToTar writes a single file to a tar archive.
func addToTar(tw *tar.Writer, path, name string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	header := &tar.Header{
		Name:    name,
		Size:    info.Size(),
		Mode:    0o644,
		ModTime: info.ModTime(),
	}

	if err := tw.WriteHeader(header); err != nil {
		return err
	}

	_, err = tw.Write(data)
	return err
}
