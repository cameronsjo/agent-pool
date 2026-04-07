package mail

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/cameronsjo/agent-pool/internal/atomicfile"
)

// Post composes a mail message and writes it atomically to the pool's postoffice.
// Creates the postoffice directory if it doesn't exist. Uses atomic writes
// to prevent partial files from being picked up by the daemon's watcher.
func Post(poolDir string, msg *Message) error {
	composed, err := Compose(msg)
	if err != nil {
		return fmt.Errorf("composing message: %w", err)
	}

	postoffice := filepath.Join(poolDir, "postoffice")
	if err := os.MkdirAll(postoffice, 0o755); err != nil {
		return fmt.Errorf("creating postoffice dir: %w", err)
	}

	path := filepath.Join(postoffice, msg.ID+".md")
	if err := atomicfile.WriteFile(path, []byte(composed)); err != nil {
		return fmt.Errorf("writing to postoffice: %w", err)
	}

	return nil
}
