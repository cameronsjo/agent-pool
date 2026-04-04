package taskboard

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/cameronsjo/agent-pool/internal/atomicfile"
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

	if err := atomicfile.WriteFile(path, data); err != nil {
		return fmt.Errorf("saving taskboard: %w", err)
	}

	return nil
}
