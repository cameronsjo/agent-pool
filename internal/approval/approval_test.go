// Approval package coverage matrix:
//
// Gate.Request (Classification: FILESYSTEM I/O + CONCURRENCY)
//   [x] Happy: approved response returns nil (TestGate_RequestApproved)
//   [x] Happy: rejected response returns error (TestGate_RequestRejected)
//   [x] Error: timeout returns error (TestGate_RequestTimeout)
//   [x] Boundary: rejected with reason includes reason (TestGate_RequestRejectedWithReason)
//
// Respond (Classification: FILESYSTEM I/O)
//   [x] Happy: approved writes .approved file (TestRespond_Approved)
//   [x] Happy: rejected writes .rejected file with reason (TestRespond_Rejected)
//   [x] Error: no proposal file returns error (TestRespond_NoProposal)
//
// StdoutPresenter (Classification: I/O)
//   [x] Happy: y input returns true (TestStdoutPresenter_Approve)
//   [x] Happy: n input returns false (TestStdoutPresenter_Reject)
//
// FilePresenter (Classification: FILESYSTEM I/O)
//   [x] Happy: .approved file returns true (TestFilePresenter_Approve)
//   [x] Happy: .rejected file returns false (TestFilePresenter_Reject)
//
// ParseHumanInbox (Classification: PURE LOGIC)
//   [x] Happy: stdout returns StdoutPresenter (TestParseHumanInbox_Stdout)
//   [x] Happy: file: returns FilePresenter (TestParseHumanInbox_File)
//   [x] Error: unsupported value (TestParseHumanInbox_Unsupported)
//
// ProposalID (Classification: PURE LOGIC)
//   [x] Happy: extracts ID from .proposal.md filename (TestProposalID)
package approval

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGate_RequestApproved(t *testing.T) {
	dir := t.TempDir()
	gate := &Gate{
		ApprovalsDir: dir,
		PollInterval: 50 * time.Millisecond,
		Timeout:      5 * time.Second,
	}

	// Respond approved after a short delay
	respondErr := make(chan error, 1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		respondErr <- Respond(dir, "task-001", true, "")
	}()

	err := gate.Request(context.Background(), "task-001", "## Proposed task\n\nDo the thing.")
	if err != nil {
		t.Fatalf("expected approval, got error: %v", err)
	}
	if err := <-respondErr; err != nil {
		t.Errorf("Respond: %v", err)
	}
}

func TestGate_RequestRejected(t *testing.T) {
	dir := t.TempDir()
	gate := &Gate{
		ApprovalsDir: dir,
		PollInterval: 50 * time.Millisecond,
		Timeout:      5 * time.Second,
	}

	respondErr := make(chan error, 1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		respondErr <- Respond(dir, "task-002", false, "")
	}()

	err := gate.Request(context.Background(), "task-002", "proposal content")
	if err == nil {
		t.Fatal("expected rejection error, got nil")
	}
	if rErr := <-respondErr; rErr != nil {
		t.Errorf("Respond: %v", rErr)
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("error = %q, want to contain 'rejected'", err.Error())
	}
}

func TestGate_RequestRejectedWithReason(t *testing.T) {
	dir := t.TempDir()
	gate := &Gate{
		ApprovalsDir: dir,
		PollInterval: 50 * time.Millisecond,
		Timeout:      5 * time.Second,
	}

	respondErr := make(chan error, 1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		respondErr <- Respond(dir, "task-003", false, "needs more detail")
	}()

	err := gate.Request(context.Background(), "task-003", "proposal")
	if err == nil {
		t.Fatal("expected rejection error")
	}
	if rErr := <-respondErr; rErr != nil {
		t.Errorf("Respond: %v", rErr)
	}
	if !strings.Contains(err.Error(), "needs more detail") {
		t.Errorf("error = %q, want to contain rejection reason", err.Error())
	}
}

func TestGate_RequestTimeout(t *testing.T) {
	dir := t.TempDir()
	gate := &Gate{
		ApprovalsDir: dir,
		PollInterval: 50 * time.Millisecond,
		Timeout:      200 * time.Millisecond,
	}

	err := gate.Request(context.Background(), "task-timeout", "proposal")
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want to contain 'timed out'", err.Error())
	}
}

func TestRespond_Approved(t *testing.T) {
	dir := t.TempDir()
	// Create proposal file
	if err := os.WriteFile(filepath.Join(dir, "task-r1.proposal.md"), []byte("proposal"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Respond(dir, "task-r1", true, ""); err != nil {
		t.Fatalf("Respond: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "task-r1.approved")); err != nil {
		t.Error("expected .approved file to exist")
	}
}

func TestRespond_Rejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "task-r2.proposal.md"), []byte("proposal"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Respond(dir, "task-r2", false, "bad plan"); err != nil {
		t.Fatalf("Respond: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "task-r2.rejected"))
	if err != nil {
		t.Fatalf("reading .rejected: %v", err)
	}
	if string(data) != "bad plan" {
		t.Errorf("rejection reason = %q, want 'bad plan'", string(data))
	}
}

func TestRespond_NoProposal(t *testing.T) {
	dir := t.TempDir()
	err := Respond(dir, "nonexistent", true, "")
	if err == nil {
		t.Fatal("expected error for missing proposal file")
	}
}

func TestStdoutPresenter_Approve(t *testing.T) {
	in := strings.NewReader("y\n")
	var out bytes.Buffer

	p := &StdoutPresenter{In: in, Out: &out}
	approved, err := p.Present(context.Background(), "task-sp1", "Do the thing")
	if err != nil {
		t.Fatalf("Present: %v", err)
	}
	if !approved {
		t.Error("expected approved=true")
	}
	if !strings.Contains(out.String(), "task-sp1") {
		t.Error("output should contain proposal ID")
	}
}

func TestStdoutPresenter_Reject(t *testing.T) {
	in := strings.NewReader("n\n")
	var out bytes.Buffer

	p := &StdoutPresenter{In: in, Out: &out}
	approved, err := p.Present(context.Background(), "task-sp2", "Do the thing")
	if err != nil {
		t.Fatalf("Present: %v", err)
	}
	if approved {
		t.Error("expected approved=false")
	}
}

func TestFilePresenter_Approve(t *testing.T) {
	reviewDir := t.TempDir()
	p := &FilePresenter{ReviewDir: reviewDir, PollInterval: 50 * time.Millisecond}

	writeErr := make(chan error, 1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		writeErr <- os.WriteFile(filepath.Join(reviewDir, "task-fp1.approved"), nil, 0o644)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	approved, err := p.Present(ctx, "task-fp1", "Do the thing")
	if err != nil {
		t.Fatalf("Present: %v", err)
	}
	if !approved {
		t.Error("expected approved=true")
	}
	if wErr := <-writeErr; wErr != nil {
		t.Errorf("writing .approved: %v", wErr)
	}
}

func TestFilePresenter_Reject(t *testing.T) {
	reviewDir := t.TempDir()
	p := &FilePresenter{ReviewDir: reviewDir, PollInterval: 50 * time.Millisecond}

	writeErr := make(chan error, 1)
	go func() {
		time.Sleep(200 * time.Millisecond)
		writeErr <- os.WriteFile(filepath.Join(reviewDir, "task-fp2.rejected"), nil, 0o644)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	approved, err := p.Present(ctx, "task-fp2", "Do the thing")
	if err != nil {
		t.Fatalf("Present: %v", err)
	}
	if approved {
		t.Error("expected approved=false")
	}
	if wErr := <-writeErr; wErr != nil {
		t.Errorf("writing .rejected: %v", wErr)
	}
}

func TestParseHumanInbox_Stdout(t *testing.T) {
	p, err := ParseHumanInbox("stdout", nil, nil)
	if err != nil {
		t.Fatalf("ParseHumanInbox: %v", err)
	}
	if _, ok := p.(*StdoutPresenter); !ok {
		t.Errorf("expected StdoutPresenter, got %T", p)
	}
}

func TestParseHumanInbox_File(t *testing.T) {
	dir := t.TempDir()
	p, err := ParseHumanInbox("file:"+dir, nil, nil)
	if err != nil {
		t.Fatalf("ParseHumanInbox: %v", err)
	}
	fp, ok := p.(*FilePresenter)
	if !ok {
		t.Fatalf("expected FilePresenter, got %T", p)
	}
	if fp.ReviewDir != dir {
		t.Errorf("ReviewDir = %q, want %q", fp.ReviewDir, dir)
	}
}

func TestParseHumanInbox_Unsupported(t *testing.T) {
	_, err := ParseHumanInbox("telegram", nil, nil)
	if err == nil {
		t.Fatal("expected error for unsupported value")
	}
}

func TestProposalID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"task-001.proposal.md", "task-001"},
		{"my-proposal.proposal.md", "my-proposal"},
		{".proposal.md", ""},
		{"no-suffix.txt", ""},
		{"task-001.pending", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := ProposalID(tt.input)
		if got != tt.want {
			t.Errorf("ProposalID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
