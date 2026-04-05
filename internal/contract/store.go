package contract

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Store manages contract files in a pool's contracts/ directory.
type Store struct {
	dir    string
	logger *slog.Logger
}

// NewStore creates a Store for the given pool directory.
// The contracts directory is {poolDir}/contracts/.
func NewStore(poolDir string) *Store {
	return &Store{
		dir:    filepath.Join(poolDir, "contracts"),
		logger: slog.Default(),
	}
}

// WithLogger sets a custom logger on the Store.
func (s *Store) WithLogger(l *slog.Logger) *Store {
	s.logger = l
	return s
}

// Save writes a contract to disk and updates the index.
// Returns an error if a contract with the same ID already exists at a
// different version (use Amend for version bumps).
func (s *Store) Save(c *Contract) error {
	if c == nil {
		return fmt.Errorf("contract is nil")
	}

	s.logger.Info("Preparing to save contract",
		"contract_id", c.ID,
		"version", c.Version,
		"between", c.Between,
	)

	path := filepath.Join(s.dir, c.ID+".md")

	// Check for existing contract — Save is for new contracts only
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("contract %q already exists; use Amend to update", c.ID)
	}

	composed, err := Compose(c)
	if err != nil {
		return fmt.Errorf("composing contract: %w", err)
	}

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("creating contracts directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(composed), 0o644); err != nil {
		return fmt.Errorf("writing contract file: %w", err)
	}

	if err := s.UpdateIndex(); err != nil {
		return fmt.Errorf("updating contract index after save: %w", err)
	}

	s.logger.Info("Successfully saved contract",
		"contract_id", c.ID,
		"version", c.Version,
		"between", c.Between,
	)

	return nil
}

// Amend loads an existing contract, increments its version, updates the body
// and timestamp, saves it, and returns the updated contract.
func (s *Store) Amend(id string, newBody string) (*Contract, error) {
	s.logger.Info("Preparing to amend contract",
		"contract_id", id,
	)

	c, err := s.Load(id)
	if err != nil {
		return nil, fmt.Errorf("loading contract for amendment: %w", err)
	}

	previousVersion := c.Version
	c.Version++
	c.Body = newBody
	c.Timestamp = time.Now().UTC()

	composed, err := Compose(c)
	if err != nil {
		return nil, fmt.Errorf("composing amended contract: %w", err)
	}

	path := filepath.Join(s.dir, c.ID+".md")
	if err := os.WriteFile(path, []byte(composed), 0o644); err != nil {
		return nil, fmt.Errorf("writing amended contract: %w", err)
	}

	if err := s.UpdateIndex(); err != nil {
		return nil, fmt.Errorf("updating index: %w", err)
	}

	s.logger.Info("Successfully amended contract",
		"contract_id", id,
		"previous_version", previousVersion,
		"new_version", c.Version,
		"between", c.Between,
	)

	return c, nil
}

// Load reads a contract by ID from the store.
func (s *Store) Load(id string) (*Contract, error) {
	path := filepath.Join(s.dir, id+".md")
	return ParseFile(path)
}

// List reads all contracts from the store directory.
func (s *Store) List() ([]*Contract, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading contracts directory: %w", err)
	}

	var contracts []*Contract
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		if entry.Name() == "index.md" {
			continue
		}
		c, err := ParseFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			s.logger.Warn("Skipping malformed contract file",
				"file", entry.Name(),
				"error", err,
			)
			continue
		}
		contracts = append(contracts, c)
	}

	sort.Slice(contracts, func(i, j int) bool {
		return contracts[i].ID < contracts[j].ID
	})

	return contracts, nil
}

// UpdateIndex rewrites contracts/index.md as a markdown table summarizing
// all contracts in the store.
func (s *Store) UpdateIndex() error {
	contracts, err := s.List()
	if err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString("# Contract Index\n\n")
	b.WriteString("| ID | Between | Version | Timestamp |\n")
	b.WriteString("|----|---------|---------|----------|\n")

	for _, c := range contracts {
		fmt.Fprintf(&b, "| %s | %s | %d | %s |\n",
			c.ID,
			strings.Join(c.Between, ", "),
			c.Version,
			c.Timestamp.Format(time.RFC3339),
		)
	}

	indexPath := filepath.Join(s.dir, "index.md")
	if err := os.WriteFile(indexPath, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("writing contract index: %w", err)
	}
	return nil
}
