// Package frontmatter provides shared YAML frontmatter parsing for markdown
// files. Both the mail and contract packages use this to split documents into
// a YAML header and a markdown body.
package frontmatter

import (
	"fmt"
	"strings"
)

// Split separates a markdown document with YAML frontmatter into its header
// and body components. The document must start with a "---" delimiter line,
// contain a YAML header, and end the header with another "---" delimiter line.
//
// Returns the raw YAML header (without delimiters) and the body (trimmed of
// leading whitespace after the closing delimiter).
func Split(content string) (header string, body string, err error) {
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
	// The closing delimiter line must contain only optional trailing whitespace
	if nl := strings.IndexByte(afterClose, '\n'); nl >= 0 {
		trailing := strings.TrimRight(afterClose[:nl], " \t")
		if trailing != "" {
			return "", "", fmt.Errorf("closing --- has trailing content: %q", afterClose[:nl])
		}
		body = afterClose[nl+1:]
	}

	return header, body, nil
}
