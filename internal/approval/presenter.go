package approval

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StdoutPresenter prints proposals to stdout and reads y/n from stdin.
type StdoutPresenter struct {
	In  io.Reader
	Out io.Writer
}

// Present displays the proposal and waits for y/n input.
func (p *StdoutPresenter) Present(ctx context.Context, proposalID, proposal string) (bool, error) {
	out := p.Out
	if out == nil {
		out = os.Stdout
	}
	in := p.In
	if in == nil {
		in = os.Stdin
	}

	separator := strings.Repeat("─", 60)
	fmt.Fprintf(out, "\n%s\n", separator)
	fmt.Fprintf(out, "APPROVAL REQUEST: %s\n", proposalID)
	fmt.Fprintf(out, "%s\n\n", separator)
	fmt.Fprintf(out, "%s\n\n", proposal)
	fmt.Fprint(out, "Approve this task dispatch? [y/n]: ")

	responseCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(in)
		if scanner.Scan() {
			responseCh <- strings.TrimSpace(strings.ToLower(scanner.Text()))
		} else {
			responseCh <- ""
		}
	}()

	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case response := <-responseCh:
		switch response {
		case "y", "yes":
			fmt.Fprintf(out, "Approved.\n\n")
			return true, nil
		default:
			fmt.Fprintf(out, "Rejected.\n\n")
			return false, nil
		}
	}
}

// FilePresenter writes proposals to a configured directory and watches
// for .approved/.rejected response files.
type FilePresenter struct {
	ReviewDir    string        // where to write proposals for human review
	PollInterval time.Duration // how often to check for response files
}

// Present writes the proposal to the review directory and polls for a response.
func (p *FilePresenter) Present(ctx context.Context, proposalID, proposal string) (bool, error) {
	reviewDir := p.ReviewDir
	if err := os.MkdirAll(reviewDir, 0o755); err != nil {
		return false, fmt.Errorf("creating review dir: %w", err)
	}

	// Write proposal to review directory
	proposalPath := filepath.Join(reviewDir, proposalID+".proposal.md")
	if err := os.WriteFile(proposalPath, []byte(proposal), 0o644); err != nil {
		return false, fmt.Errorf("writing proposal to review dir: %w", err)
	}

	pollInterval := p.PollInterval
	if pollInterval == 0 {
		pollInterval = 2 * time.Second
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	approvedPath := filepath.Join(reviewDir, proposalID+".approved")
	rejectedPath := filepath.Join(reviewDir, proposalID+".rejected")

	for {
		select {
		case <-ctx.Done():
			os.Remove(proposalPath)
			return false, ctx.Err()
		case <-ticker.C:
			if _, err := os.Stat(approvedPath); err == nil {
				os.Remove(proposalPath)
				return true, nil
			}
			if _, err := os.Stat(rejectedPath); err == nil {
				os.Remove(proposalPath)
				return false, nil
			}
		}
	}
}

// ParseHumanInbox parses the human_inbox config value and returns the
// appropriate Presenter. Supported values:
//   - "stdout" → StdoutPresenter
//   - "file:/path/to/reviews/" → FilePresenter
func ParseHumanInbox(value string, stdin io.Reader, stdout io.Writer) (Presenter, error) {
	switch {
	case value == "stdout" || value == "":
		return &StdoutPresenter{In: stdin, Out: stdout}, nil
	case strings.HasPrefix(value, "file:"):
		dir := strings.TrimPrefix(value, "file:")
		// Expand ~
		if strings.HasPrefix(dir, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("expanding ~ in human_inbox: %w", err)
			}
			dir = filepath.Join(home, dir[2:])
		}
		return &FilePresenter{ReviewDir: dir}, nil
	default:
		return nil, fmt.Errorf("unsupported human_inbox value %q: use \"stdout\" or \"file:/path/\"", value)
	}
}
