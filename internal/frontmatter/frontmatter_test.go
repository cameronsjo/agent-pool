// Frontmatter Split coverage matrix:
//
// Split (Classification: PURE LOGIC)
//   [x] Happy: valid frontmatter with body (TestSplit_ValidWithBody)
//   [x] Happy: valid frontmatter with empty body (TestSplit_EmptyBody)
//   [x] Happy: leading whitespace before opening delimiter (TestSplit_LeadingWhitespace)
//   [x] Error: no opening delimiter (TestSplit_NoOpeningDelimiter)
//   [x] Error: no closing delimiter (TestSplit_NoClosingDelimiter)
//   [x] Error: opening delimiter not followed by newline (TestSplit_NoNewlineAfterOpening)
//   [x] Boundary: empty content (TestSplit_EmptyContent)
package frontmatter

import (
	"testing"
)

func TestSplit_ValidWithBody(t *testing.T) {
	content := "---\nid: test-001\nfrom: arch\n---\n\n## Task body\n"
	header, body, err := Split(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if header != "id: test-001\nfrom: arch" {
		t.Errorf("header = %q, want %q", header, "id: test-001\nfrom: arch")
	}
	if body != "\n## Task body\n" {
		t.Errorf("body = %q, want %q", body, "\n## Task body\n")
	}
}

func TestSplit_EmptyBody(t *testing.T) {
	content := "---\nid: test-001\n---\n"
	header, body, err := Split(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if header != "id: test-001" {
		t.Errorf("header = %q, want %q", header, "id: test-001")
	}
	if body != "" {
		t.Errorf("body = %q, want empty", body)
	}
}

func TestSplit_LeadingWhitespace(t *testing.T) {
	content := "  \n---\nkey: value\n---\n\nbody\n"
	header, _, err := Split(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if header != "key: value" {
		t.Errorf("header = %q, want %q", header, "key: value")
	}
}

func TestSplit_NoOpeningDelimiter(t *testing.T) {
	_, _, err := Split("no frontmatter here")
	if err == nil {
		t.Fatal("expected error for missing opening delimiter")
	}
}

func TestSplit_NoClosingDelimiter(t *testing.T) {
	_, _, err := Split("---\nid: test\n")
	if err == nil {
		t.Fatal("expected error for missing closing delimiter")
	}
}

func TestSplit_NoNewlineAfterOpening(t *testing.T) {
	_, _, err := Split("---id: inline")
	if err == nil {
		t.Fatal("expected error for missing newline after opening delimiter")
	}
}

func TestSplit_EmptyContent(t *testing.T) {
	_, _, err := Split("")
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}
