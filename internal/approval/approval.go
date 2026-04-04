// Package approval implements the human approval gate for architect task
// dispatch. The architect's MCP tool handler writes a proposal to the
// approvals directory, and the daemon presents it to the human for review.
// Communication between the two processes uses the filesystem.
//
// Protocol:
//  1. Tool handler writes {id}.proposal.md (the task details — also triggers daemon via fsnotify)
//  2. Tool handler polls for {id}.approved or {id}.rejected
//  3. Daemon detects .proposal.md, reads it, presents to human
//  4. Daemon writes .approved or .rejected based on human response
//  5. Tool handler reads response, proceeds or returns error
package approval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Presenter abstracts how proposals are shown to humans for approval.
// The daemon selects an implementation based on the human_inbox config.
type Presenter interface {
	Present(ctx context.Context, proposalID, proposal string) (approved bool, err error)
}

// Gate manages the approval workflow from the MCP tool handler side.
// It writes proposals and polls for responses.
type Gate struct {
	ApprovalsDir string
	PollInterval time.Duration
	Timeout      time.Duration
}

// DefaultGate returns a Gate with sensible defaults.
func DefaultGate(poolDir string) *Gate {
	return &Gate{
		ApprovalsDir: filepath.Join(poolDir, "approvals"),
		PollInterval: 2 * time.Second,
		Timeout:      5 * time.Minute,
	}
}

// Request writes a proposal to the approvals directory and polls for a
// response. It blocks until approved, rejected, or the context is cancelled.
// Returns nil if approved, an error if rejected or timed out.
func (g *Gate) Request(ctx context.Context, proposalID, proposal string) error {
	if err := os.MkdirAll(g.ApprovalsDir, 0o755); err != nil {
		return fmt.Errorf("creating approvals dir: %w", err)
	}

	// Write proposal content — this also triggers the daemon via fsnotify
	// (the file ends in .md, which the watcher accepts)
	proposalPath := filepath.Join(g.ApprovalsDir, proposalID+".proposal.md")
	if err := os.WriteFile(proposalPath, []byte(proposal), 0o644); err != nil {
		return fmt.Errorf("writing proposal: %w", err)
	}

	// Poll for response
	approvedPath := filepath.Join(g.ApprovalsDir, proposalID+".approved")
	rejectedPath := filepath.Join(g.ApprovalsDir, proposalID+".rejected")

	timeout := g.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	pollInterval := g.PollInterval
	if pollInterval == 0 {
		pollInterval = 2 * time.Second
	}

	deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-deadlineCtx.Done():
			// Clean up proposal files on timeout
			os.Remove(proposalPath)
			
			return fmt.Errorf("approval timed out after %v", timeout)

		case <-ticker.C:
			if _, err := os.Stat(approvedPath); err == nil {
				// Clean up all approval files
				os.Remove(proposalPath)
				
				os.Remove(approvedPath)
				return nil
			}
			if _, err := os.Stat(rejectedPath); err == nil {
				// Read rejection reason if available
				reason, _ := os.ReadFile(rejectedPath)
				// Clean up
				os.Remove(proposalPath)
				
				os.Remove(rejectedPath)
				if len(reason) > 0 {
					return fmt.Errorf("proposal rejected: %s", string(reason))
				}
				return fmt.Errorf("proposal rejected")
			}
		}
	}
}

// Respond writes an approval or rejection response for a proposal.
// Called by the daemon after the human reviews.
func Respond(approvalsDir, proposalID string, approved bool, reason string) error {
	proposalPath := filepath.Join(approvalsDir, proposalID+".proposal.md")
	if _, err := os.Stat(proposalPath); err != nil {
		return fmt.Errorf("no proposal %q: %w", proposalID, err)
	}

	if approved {
		path := filepath.Join(approvalsDir, proposalID+".approved")
		return os.WriteFile(path, nil, 0o644)
	}

	path := filepath.Join(approvalsDir, proposalID+".rejected")
	return os.WriteFile(path, []byte(reason), 0o644)
}

// ReadProposal reads the content of a pending proposal.
func ReadProposal(approvalsDir, proposalID string) (string, error) {
	path := filepath.Join(approvalsDir, proposalID+".proposal.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading proposal: %w", err)
	}
	return string(data), nil
}

// ProposalID extracts the proposal ID from a .proposal.md filename.
// Returns empty string if the file doesn't match the pattern.
func ProposalID(filename string) string {
	const suffix = ".proposal.md"
	if len(filename) <= len(suffix) {
		return ""
	}
	if filename[len(filename)-len(suffix):] != suffix {
		return ""
	}
	return filename[:len(filename)-len(suffix)]
}
