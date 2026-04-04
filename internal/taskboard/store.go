package taskboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Load reads a Board from the given JSON file path.
// If the file does not exist, an empty board is returned.
func Load(path string) (*Board, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}
		return nil, fmt.Errorf("reading taskboard: %w", err)
	}

	var b Board
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("parsing taskboard: %w", err)
	}

	if b.Tasks == nil {
		b.Tasks = make(map[string]*Task)
	}

	return &b, nil
}

// Save writes the board to the given path atomically (temp file + rename).
func (b *Board) Save(path string) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling taskboard: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".taskboard-*.json")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("syncing temp file: %w", err)
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
