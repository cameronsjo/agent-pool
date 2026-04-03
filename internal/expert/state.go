package expert

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxStateSize is the maximum allowed size for state.md content in bytes.
// Prevents runaway state growth from unbounded expert updates.
const MaxStateSize = 50_000

// ReadState reads the three expert state files (identity.md, state.md, errors.md).
// Missing files return empty strings — a cold-start expert may not have all files yet.
func ReadState(expertDir string) (identity, state, errors string, err error) {
	identity, err = readOptionalFile(expertDir, "identity.md")
	if err != nil {
		return "", "", "", fmt.Errorf("reading identity.md: %w", err)
	}

	state, err = readOptionalFile(expertDir, "state.md")
	if err != nil {
		return "", "", "", fmt.Errorf("reading state.md: %w", err)
	}

	errors, err = readOptionalFile(expertDir, "errors.md")
	if err != nil {
		return "", "", "", fmt.Errorf("reading errors.md: %w", err)
	}

	return identity, state, errors, nil
}

// WriteState validates and atomically writes state.md content.
// Content must be non-empty and not exceed MaxStateSize bytes.
func WriteState(expertDir, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("state content is empty")
	}
	if len(content) > MaxStateSize {
		return fmt.Errorf("state content exceeds maximum size (%d > %d bytes)", len(content), MaxStateSize)
	}

	path := filepath.Join(expertDir, "state.md")

	tmp, err := os.CreateTemp(expertDir, ".state-*.md")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.WriteString(content + "\n"); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming temp file: %w", err)
	}

	return nil
}

// AppendError appends a structured, timestamp-prefixed error entry to errors.md.
// Creates the file if it doesn't exist.
func AppendError(expertDir, entry string) error {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return fmt.Errorf("error entry is empty")
	}

	path := filepath.Join(expertDir, "errors.md")

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening errors.md: %w", err)
	}
	defer f.Close()

	timestamp := time.Now().UTC().Format(time.RFC3339)
	formatted := fmt.Sprintf("\n### %s\n\n%s\n", timestamp, entry)

	if _, err := f.WriteString(formatted); err != nil {
		return fmt.Errorf("appending to errors.md: %w", err)
	}

	return nil
}

// ReadLog reads a specific task log by ID. Returns the raw JSON bytes.
// The taskID is validated to prevent path traversal.
func ReadLog(expertDir, taskID string) ([]byte, error) {
	if err := validateTaskID(taskID); err != nil {
		return nil, err
	}

	path := filepath.Join(expertDir, "logs", taskID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("task log not found: %s", taskID)
		}
		return nil, fmt.Errorf("reading task log: %w", err)
	}

	return data, nil
}

// SearchIndex searches logs/index.md for rows matching the query string.
// Matching is case-insensitive substring across all columns.
// Returns matching table rows (without the header).
func SearchIndex(expertDir, query string) ([]string, error) {
	path := filepath.Join(expertDir, "logs", "index.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no index yet
		}
		return nil, fmt.Errorf("reading index: %w", err)
	}

	queryLower := strings.ToLower(query)
	lines := strings.Split(string(data), "\n")
	var matches []string

	for i, line := range lines {
		// Skip header (first two lines: column names + separator)
		if i < 2 {
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(strings.ToLower(line), queryLower) {
			matches = append(matches, line)
		}
	}

	return matches, nil
}

// validateTaskID ensures a task ID is safe to use as a filename component.
func validateTaskID(taskID string) error {
	if taskID == "" {
		return fmt.Errorf("task ID is empty")
	}
	if taskID != filepath.Base(taskID) || taskID == "." || taskID == ".." {
		return fmt.Errorf("invalid task ID %q: must be a simple filename", taskID)
	}
	return nil
}
