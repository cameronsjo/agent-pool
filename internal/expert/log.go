package expert

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LogEntry represents one row in logs/index.md.
type LogEntry struct {
	TaskID    string
	Timestamp time.Time
	From      string
	ExitCode  int
	Summary   string
}

const indexHeader = "| Task ID | Timestamp | From | Exit | Summary |\n|---------|-----------|------|-----:|---------|\n"

// WriteLog writes raw session output to logs/{task-id}.json.
func WriteLog(expertDir string, taskID string, output []byte) error {
	logsDir := filepath.Join(expertDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return fmt.Errorf("creating logs directory: %w", err)
	}

	path := filepath.Join(logsDir, taskID+".json")
	if err := os.WriteFile(path, output, 0o644); err != nil {
		return fmt.Errorf("writing log file: %w", err)
	}

	return nil
}

// AppendIndex appends a summary row to logs/index.md.
// Creates the file with a markdown table header if it doesn't exist.
func AppendIndex(expertDir string, entry *LogEntry) error {
	logsDir := filepath.Join(expertDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return fmt.Errorf("creating logs directory: %w", err)
	}

	indexPath := filepath.Join(logsDir, "index.md")

	f, err := os.OpenFile(indexPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening index file: %w", err)
	}
	defer f.Close()

	// Write header if file is new (empty)
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat index file: %w", err)
	}
	if info.Size() == 0 {
		if _, err := f.WriteString(indexHeader); err != nil {
			return fmt.Errorf("writing index header: %w", err)
		}
	}

	// Sanitize summary for table cell (collapse newlines, escape pipes)
	summary := strings.ReplaceAll(entry.Summary, "\n", " ")
	summary = strings.ReplaceAll(summary, "|", "\\|")

	row := fmt.Sprintf("| %s | %s | %s | %d | %s |\n",
		entry.TaskID,
		entry.Timestamp.UTC().Format(time.RFC3339),
		entry.From,
		entry.ExitCode,
		summary,
	)

	if _, err := f.WriteString(row); err != nil {
		return fmt.Errorf("writing index row: %w", err)
	}

	return nil
}

// WriteStderr writes raw stderr output to logs/{task-id}.stderr.
func WriteStderr(expertDir string, taskID string, stderr []byte) error {
	logsDir := filepath.Join(expertDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return fmt.Errorf("creating logs directory: %w", err)
	}

	path := filepath.Join(logsDir, taskID+".stderr")
	if err := os.WriteFile(path, stderr, 0o644); err != nil {
		return fmt.Errorf("writing stderr file: %w", err)
	}

	return nil
}

// streamJSONMessage represents one line of claude's --output-format stream-json.
type streamJSONMessage struct {
	Type   string `json:"type"`
	Result string `json:"result"`
}

// ExtractSummary parses stream-json output for the final result text.
// Returns the first 200 characters of the result. Falls back to a placeholder
// if no result message is found.
func ExtractSummary(output []byte) string {
	const maxLen = 200
	const fallback = "(no summary available)"

	var lastResult string

	// stream-json is newline-delimited JSON
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var msg streamJSONMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}

		if msg.Type == "result" && msg.Result != "" {
			lastResult = msg.Result
		}
	}

	if lastResult == "" {
		return fallback
	}

	// Collapse whitespace and truncate
	lastResult = strings.Join(strings.Fields(lastResult), " ")
	if lastResult == "" {
		return fallback
	}
	if len(lastResult) > maxLen {
		lastResult = lastResult[:maxLen] + "..."
	}

	return lastResult
}
