package mail

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Compose builds a raw mail file string from a Message.
// This is the inverse of Parse — YAML frontmatter + markdown body.
// Required fields (ID, From, To, Type) are validated before composing.
func Compose(msg *Message) (string, error) {
	if msg == nil {
		return "", fmt.Errorf("message is nil")
	}
	if msg.ID == "" {
		return "", fmt.Errorf("missing required field: id")
	}
	if msg.From == "" {
		return "", fmt.Errorf("missing required field: from")
	}
	if msg.To == "" {
		return "", fmt.Errorf("missing required field: to")
	}
	if msg.Type == "" {
		return "", fmt.Errorf("missing required field: type")
	}

	// Default priority if not set
	priority := msg.Priority
	if priority == "" {
		priority = PriorityNormal
	}

	// Default timestamp if zero
	timestamp := msg.Timestamp
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}

	// Build the YAML-serializable header
	header := composeHeader{
		ID:        msg.ID,
		From:      msg.From,
		To:        msg.To,
		Type:      msg.Type,
		Contracts: msg.Contracts,
		Priority:  priority,
		DependsOn: msg.DependsOn,
		Timestamp: timestamp,
	}

	yamlBytes, err := yaml.Marshal(&header)
	if err != nil {
		return "", fmt.Errorf("marshaling YAML header: %w", err)
	}

	var b strings.Builder
	b.WriteString("---\n")
	b.Write(yamlBytes)
	b.WriteString("---\n")

	if msg.Body != "" {
		b.WriteString("\n")
		b.WriteString(msg.Body)
		b.WriteString("\n")
	}

	return b.String(), nil
}

// composeHeader is the YAML-serializable form of the mail frontmatter.
// Separate from Message to control field ordering and omitempty behavior.
type composeHeader struct {
	ID        string      `yaml:"id"`
	From      string      `yaml:"from"`
	To        string      `yaml:"to"`
	Type      MessageType `yaml:"type"`
	Contracts []string    `yaml:"contracts,omitempty"`
	Priority  Priority    `yaml:"priority"`
	DependsOn []string    `yaml:"depends-on,omitempty"`
	Timestamp time.Time   `yaml:"timestamp"`
}
