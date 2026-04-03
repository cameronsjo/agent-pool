// Package mail handles parsing, routing, and delivery of agent-pool messages.
//
// Messages are markdown files with YAML frontmatter. The router reads the
// "to" header and copies messages from the postoffice to the target agent's inbox.
package mail

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// MessageType enumerates the kinds of mail messages.
type MessageType string

const (
	TypeTask     MessageType = "task"
	TypeQuestion MessageType = "question"
	TypeResponse MessageType = "response"
	TypeNotify   MessageType = "notify"
	TypeHandoff  MessageType = "handoff"
	TypeCancel   MessageType = "cancel"
)

// Priority enumerates message urgency levels.
type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
	PriorityUrgent Priority = "urgent"
)

// Message represents a parsed mail file (YAML frontmatter + markdown body).
type Message struct {
	ID        string      `yaml:"id"`
	From      string      `yaml:"from"`
	To        string      `yaml:"to"`
	Type      MessageType `yaml:"type"`
	Contracts []string    `yaml:"contracts,omitempty"`
	Priority  Priority    `yaml:"priority"`
	DependsOn []string    `yaml:"depends-on,omitempty"`
	Timestamp time.Time   `yaml:"timestamp"`

	// Body is the markdown content after the frontmatter.
	Body string `yaml:"-"`

	// SourcePath is the file this message was parsed from.
	SourcePath string `yaml:"-"`
}

// ParseFile reads a markdown file with YAML frontmatter and returns a Message.
//
// The expected format is:
//
//	---
//	id: task-042
//	from: architect
//	to: auth
//	type: task
//	---
//
//	## Task description here
func ParseFile(path string) (*Message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading mail file: %w", err)
	}

	return Parse(string(data), path)
}

// Parse parses a mail message from its raw string content.
// sourcePath is recorded on the returned Message for reference.
func Parse(content string, sourcePath string) (*Message, error) {
	header, body, err := splitFrontmatter(content)
	if err != nil {
		return nil, fmt.Errorf("splitting frontmatter: %w", err)
	}

	var msg Message
	if err := yaml.Unmarshal([]byte(header), &msg); err != nil {
		return nil, fmt.Errorf("parsing YAML frontmatter: %w", err)
	}

	if msg.ID == "" {
		return nil, fmt.Errorf("missing required field: id")
	}
	if msg.From == "" {
		return nil, fmt.Errorf("missing required field: from")
	}
	if msg.To == "" {
		return nil, fmt.Errorf("missing required field: to")
	}
	if msg.Type == "" {
		return nil, fmt.Errorf("missing required field: type")
	}

	if msg.Priority == "" {
		msg.Priority = PriorityNormal
	}

	msg.Body = strings.TrimSpace(body)
	msg.SourcePath = sourcePath

	return &msg, nil
}

// splitFrontmatter splits content at --- delimiters into YAML header and body.
func splitFrontmatter(content string) (header string, body string, err error) {
	const delimiter = "---"

	trimmed := strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(trimmed, delimiter) {
		return "", "", fmt.Errorf("content does not start with --- delimiter")
	}

	// Skip the opening delimiter
	rest := trimmed[len(delimiter):]
	rest = strings.TrimLeft(rest, " \t")
	if len(rest) == 0 || rest[0] != '\n' {
		return "", "", fmt.Errorf("opening --- must be followed by a newline")
	}
	rest = rest[1:]

	// Find the closing delimiter
	idx := strings.Index(rest, "\n"+delimiter)
	if idx < 0 {
		return "", "", fmt.Errorf("no closing --- delimiter found")
	}

	header = rest[:idx]

	// Body starts after the closing delimiter line
	afterClose := rest[idx+1+len(delimiter):]
	// Skip rest of the delimiter line (could have trailing spaces)
	if nl := strings.IndexByte(afterClose, '\n'); nl >= 0 {
		body = afterClose[nl+1:]
	}

	return header, body, nil
}
