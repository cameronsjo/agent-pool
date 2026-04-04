// Package contract manages architect-defined interface specifications between
// experts. Contracts are markdown files with YAML frontmatter stored in the
// pool's contracts/ directory. They follow the same frontmatter pattern as
// the mail package.
package contract

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"git.sjo.lol/cameron/agent-pool/internal/frontmatter"
)

// Contract represents a parsed contract file (YAML frontmatter + markdown body).
type Contract struct {
	ID        string    `yaml:"id"`
	Type      string    `yaml:"type"`
	DefinedBy string    `yaml:"defined-by"`
	Between   []string  `yaml:"between"`
	Version   int       `yaml:"version"`
	Timestamp time.Time `yaml:"timestamp"`

	// Body is the markdown content after the frontmatter.
	Body string `yaml:"-"`
}

// ParseFile reads a contract markdown file and returns a Contract.
func ParseFile(path string) (*Contract, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading contract file: %w", err)
	}
	return Parse(string(data))
}

// Parse parses a contract from its raw string content.
func Parse(content string) (*Contract, error) {
	header, body, err := frontmatter.Split(content)
	if err != nil {
		return nil, fmt.Errorf("splitting frontmatter: %w", err)
	}

	var c Contract
	if err := yaml.Unmarshal([]byte(header), &c); err != nil {
		return nil, fmt.Errorf("parsing YAML frontmatter: %w", err)
	}

	if err := c.validate(); err != nil {
		return nil, err
	}

	c.Body = strings.TrimSpace(body)
	return &c, nil
}

// Compose builds a raw contract file string from a Contract.
func Compose(c *Contract) (string, error) {
	if c == nil {
		return "", fmt.Errorf("contract is nil")
	}
	if err := c.validate(); err != nil {
		return "", err
	}

	header := composeHeader{
		ID:        c.ID,
		Type:      c.Type,
		DefinedBy: c.DefinedBy,
		Between:   c.Between,
		Version:   c.Version,
		Timestamp: c.Timestamp,
	}

	yamlBytes, err := yaml.Marshal(&header)
	if err != nil {
		return "", fmt.Errorf("marshaling YAML header: %w", err)
	}

	var b strings.Builder
	b.WriteString("---\n")
	b.Write(yamlBytes)
	b.WriteString("---\n")

	if c.Body != "" {
		b.WriteString("\n")
		b.WriteString(c.Body)
		b.WriteString("\n")
	}

	return b.String(), nil
}

func (c *Contract) validate() error {
	if c.ID == "" {
		return fmt.Errorf("missing required field: id")
	}
	if c.ID != filepath.Base(c.ID) || c.ID == "." || c.ID == ".." {
		return fmt.Errorf("invalid contract ID %q: must be a simple filename", c.ID)
	}
	if c.Type != "contract" {
		return fmt.Errorf("invalid type %q: must be \"contract\"", c.Type)
	}
	if len(c.Between) < 2 {
		return fmt.Errorf("between must list at least 2 parties, got %d", len(c.Between))
	}
	if c.Version < 1 {
		return fmt.Errorf("version must be >= 1, got %d", c.Version)
	}
	return nil
}

// composeHeader controls YAML field ordering and omitempty behavior.
type composeHeader struct {
	ID        string    `yaml:"id"`
	Type      string    `yaml:"type"`
	DefinedBy string    `yaml:"defined-by"`
	Between   []string  `yaml:"between"`
	Version   int       `yaml:"version"`
	Timestamp time.Time `yaml:"timestamp"`
}
