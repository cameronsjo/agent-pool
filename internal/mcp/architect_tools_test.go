// Architect tools coverage matrix:
//
// RegisterArchitectTools (Classification: INTEGRATION)
//   [x] Happy: all 4 architect tools + 6 expert tools registered (TestArchitectTools_Registration)
//
//define_contract (Classification: FILESYSTEM I/O)
//   [x] Happy: creates contract file and index (TestDefineContract_Happy)
//   [x] Error: missing params (TestDefineContract_MissingParams)
//   [x] Error: fewer than 2 between parties (TestDefineContract_TooFewBetween)
//
//send_task (Classification: FILESYSTEM I/O)
//   [x] Happy: message appears in postoffice (TestSendTask_Happy)
//   [x] Error: missing params (TestSendTask_MissingParams)
//   [x] Error: path traversal ID (TestSendTask_PathTraversal)
//
//verify_result (Classification: FILESYSTEM I/O)
//   [x] Happy: verification log created (TestVerifyResult_Happy)
//   [x] Error: invalid status (TestVerifyResult_InvalidStatus)
//   [x] Error: contract not found (TestVerifyResult_ContractNotFound)
//
//amend_contract (Classification: FILESYSTEM I/O)
//   [x] Happy: version incremented + notify messages (TestAmendContract_Happy)
//   [x] Error: contract not found (TestAmendContract_NotFound)
//
// Approval gate integration (Classification: FILESYSTEM I/O + CONCURRENCY)
//   [x] Happy: none mode bypasses approval (TestSendTask_ApprovalNoneMode)
//   [x] Happy: decomposition mode blocks until approved (TestSendTask_ApprovalRequired)
//   [x] Unhappy: rejected proposal returns error, task not dispatched (TestSendTask_ApprovalRejected)

package mcp_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/cameronsjo/agent-pool/internal/approval"
	"github.com/cameronsjo/agent-pool/internal/contract"
	"github.com/cameronsjo/agent-pool/internal/mail"
	agentmcp "github.com/cameronsjo/agent-pool/internal/mcp"
)

// setupArchitectPool creates a pool directory with architect-relevant dirs.
func setupArchitectPool(t *testing.T) string {
	t.Helper()
	return makePoolDirs(t,
		"postoffice", "contracts",
		"architect/inbox", "architect/verifications",
	)
}

// buildArchitectTestServer creates an MCP server with architect + expert tools.
func buildArchitectTestServer(t *testing.T, poolDir string) *server.MCPServer {
	t.Helper()
	return buildMCPTestServer(t, poolDir, "architect", "architect")
}

func TestArchitectTools_Registration(t *testing.T) {
	poolDir := setupArchitectPool(t)
	srv := buildArchitectTestServer(t, poolDir)

	tools := listToolNames(t, srv)

	expected := []string{
		// Architect tools
		"define_contract", "send_task",
		"verify_result", "amend_contract",
		// Expert tools (inherited)
		"read_state", "update_state",
		"append_error", "send_response",
		"recall", "search_index",
	}
	for _, name := range expected {
		if !tools[name] {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestDefineContract_Happy(t *testing.T) {
	poolDir := setupArchitectPool(t)
	srv := buildArchitectTestServer(t, poolDir)

	result := callTool(t, srv, "define_contract", map[string]any{
		"id":      "contract-001",
		"between": "auth, frontend",
		"body":    "## Token Exchange\n\nSpec goes here.",
	})

	text := resultText(t, result)
	if !strings.Contains(text, "contract-001") {
		t.Errorf("result %q does not mention contract ID", text)
	}

	// Verify file
	c, err := contract.ParseFile(filepath.Join(poolDir, "contracts", "contract-001.md"))
	if err != nil {
		t.Fatalf("parsing contract: %v", err)
	}
	if c.Version != 1 {
		t.Errorf("version = %d, want 1", c.Version)
	}
	if len(c.Between) != 2 {
		t.Errorf("between length = %d, want 2", len(c.Between))
	}
}

func TestDefineContract_MissingParams(t *testing.T) {
	poolDir := setupArchitectPool(t)
	srv := buildArchitectTestServer(t, poolDir)

	result := callTool(t, srv, "define_contract", map[string]any{
		"between": "auth, frontend",
		"body":    "spec",
	})
	if !result.IsError {
		t.Error("expected error for missing id")
	}
}

func TestDefineContract_TooFewBetween(t *testing.T) {
	poolDir := setupArchitectPool(t)
	srv := buildArchitectTestServer(t, poolDir)

	result := callTool(t, srv, "define_contract", map[string]any{
		"id":      "c1",
		"between": "only-one",
		"body":    "spec",
	})
	text := resultText(t, result)
	if !strings.Contains(text, "at least 2") {
		t.Errorf("expected 'at least 2' error, got %q", text)
	}
}

func TestSendTask_Happy(t *testing.T) {
	poolDir := setupArchitectPool(t)
	srv := buildArchitectTestServer(t, poolDir)

	result := callTool(t, srv, "send_task", map[string]any{
		"to":        "auth",
		"body":      "Implement the token endpoint",
		"id":        "task-001",
		"contracts": "contract-001",
		"priority":  "high",
	})

	text := resultText(t, result)
	if !strings.Contains(text, "task-001") {
		t.Errorf("result %q does not mention task ID", text)
	}

	// Verify message in postoffice
	msg, err := mail.ParseFile(filepath.Join(poolDir, "postoffice", "task-001.md"))
	if err != nil {
		t.Fatalf("parsing posted message: %v", err)
	}

	if msg.From != "architect" {
		t.Errorf("from = %q, want architect", msg.From)
	}
	if msg.To != "auth" {
		t.Errorf("to = %q, want auth", msg.To)
	}
	if msg.Type != mail.TypeTask {
		t.Errorf("type = %q, want task", msg.Type)
	}
	if len(msg.Contracts) != 1 || msg.Contracts[0] != "contract-001" {
		t.Errorf("contracts = %v, want [contract-001]", msg.Contracts)
	}
	if msg.Priority != mail.PriorityHigh {
		t.Errorf("priority = %q, want high", msg.Priority)
	}
}

func TestSendTask_MissingParams(t *testing.T) {
	poolDir := setupArchitectPool(t)
	srv := buildArchitectTestServer(t, poolDir)

	result := callTool(t, srv, "send_task", map[string]any{
		"to":   "auth",
		"body": "do something",
	})
	if !result.IsError {
		t.Error("expected error for missing id")
	}
}

func TestSendTask_PathTraversal(t *testing.T) {
	poolDir := setupArchitectPool(t)
	srv := buildArchitectTestServer(t, poolDir)

	result := callTool(t, srv, "send_task", map[string]any{
		"to":   "auth",
		"body": "do something",
		"id":   "../escape",
	})
	text := resultText(t, result)
	if !strings.Contains(text, "invalid message ID") {
		t.Errorf("expected path traversal error, got %q", text)
	}
}

func TestVerifyResult_Happy(t *testing.T) {
	poolDir := setupArchitectPool(t)
	srv := buildArchitectTestServer(t, poolDir)

	// Create a contract first
	callTool(t, srv, "define_contract", map[string]any{
		"id":      "contract-001",
		"between": "auth, frontend",
		"body":    "spec",
	})

	result := callTool(t, srv, "verify_result", map[string]any{
		"task_id":     "task-001",
		"contract_id": "contract-001",
		"status":      "pass",
		"notes":       "All endpoints match the contract spec.",
	})

	text := resultText(t, result)
	if !strings.Contains(text, "pass") {
		t.Errorf("result %q does not mention pass", text)
	}

	// Verify log file
	path := filepath.Join(poolDir, "architect", "verifications", "task-001_contract-001.md")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("verification file not found: %v", err)
	}
}

func TestVerifyResult_InvalidStatus(t *testing.T) {
	poolDir := setupArchitectPool(t)
	srv := buildArchitectTestServer(t, poolDir)

	result := callTool(t, srv, "verify_result", map[string]any{
		"task_id":     "task-001",
		"contract_id": "contract-001",
		"status":      "unknown",
		"notes":       "notes",
	})
	text := resultText(t, result)
	if !strings.Contains(text, "invalid status") {
		t.Errorf("expected invalid status error, got %q", text)
	}
}

func TestVerifyResult_ContractNotFound(t *testing.T) {
	poolDir := setupArchitectPool(t)
	srv := buildArchitectTestServer(t, poolDir)

	result := callTool(t, srv, "verify_result", map[string]any{
		"task_id":     "task-001",
		"contract_id": "nonexistent",
		"status":      "pass",
		"notes":       "notes",
	})
	text := resultText(t, result)
	if !strings.Contains(text, "not found") {
		t.Errorf("expected not found error, got %q", text)
	}
}

func TestAmendContract_Happy(t *testing.T) {
	poolDir := setupArchitectPool(t)
	srv := buildArchitectTestServer(t, poolDir)

	// Create initial contract
	callTool(t, srv, "define_contract", map[string]any{
		"id":      "contract-001",
		"between": "auth, frontend",
		"body":    "v1 spec",
	})

	result := callTool(t, srv, "amend_contract", map[string]any{
		"id":   "contract-001",
		"body": "## Updated spec v2\n\nNew content.",
	})

	text := resultText(t, result)
	if !strings.Contains(text, "v2") {
		t.Errorf("result %q does not mention v2", text)
	}

	// Verify version bumped
	c, err := contract.ParseFile(filepath.Join(poolDir, "contracts", "contract-001.md"))
	if err != nil {
		t.Fatalf("loading amended contract: %v", err)
	}
	if c.Version != 2 {
		t.Errorf("version = %d, want 2", c.Version)
	}

	// Verify notify messages in postoffice
	entries, err := os.ReadDir(filepath.Join(poolDir, "postoffice"))
	if err != nil {
		t.Fatalf("reading postoffice: %v", err)
	}

	notifyCount := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "notify-") {
			notifyCount++
			msg, parseErr := mail.ParseFile(filepath.Join(poolDir, "postoffice", e.Name()))
			if parseErr != nil {
				t.Errorf("parsing notify %s: %v", e.Name(), parseErr)
				continue
			}
			if msg.Type != mail.TypeNotify {
				t.Errorf("notify type = %q, want notify", msg.Type)
			}
			if msg.From != "architect" {
				t.Errorf("notify from = %q, want architect", msg.From)
			}
		}
	}
	if notifyCount != 2 {
		t.Errorf("notify count = %d, want 2 (one per party)", notifyCount)
	}
}

func TestAmendContract_NotFound(t *testing.T) {
	poolDir := setupArchitectPool(t)
	srv := buildArchitectTestServer(t, poolDir)

	result := callTool(t, srv, "amend_contract", map[string]any{
		"id":   "nonexistent",
		"body": "new body",
	})
	if !result.IsError {
		t.Error("expected error for nonexistent contract")
	}
}

// --- Approval gate tests ---

func TestSendTask_ApprovalNoneMode(t *testing.T) {
	poolDir := setupArchitectPool(t)
	if err := os.MkdirAll(filepath.Join(poolDir, "approvals"), 0o755); err != nil {
		t.Fatal(err)
	}

	// approval_mode = "none" (set in setupArchitectPool via buildArchitectTestServer)
	srv := buildArchitectTestServer(t, poolDir)

	result := callTool(t, srv, "send_task", map[string]any{
		"to":   "auth",
		"body": "do something",
		"id":   "task-none-001",
	})

	text := resultText(t, result)
	if !strings.Contains(text, "task-none-001") {
		t.Errorf("expected task sent, got %q", text)
	}

	// Verify message went directly to postoffice (no approval files)
	if _, err := os.Stat(filepath.Join(poolDir, "postoffice", "task-none-001.md")); err != nil {
		t.Error("task should be in postoffice with approval_mode=none")
	}
}

func TestSendTask_ApprovalRequired(t *testing.T) {
	poolDir := setupArchitectPool(t)
	if err := os.MkdirAll(filepath.Join(poolDir, "approvals"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Build server with decomposition mode
	cfg := &agentmcp.ServerConfig{
		PoolDir:      poolDir,
		ExpertName:   "architect",
		Role:         "architect",
		ApprovalMode: "decomposition",
		Logger:       slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	srv := server.NewMCPServer("agent-pool-test", "0.4.0-test")
	agentmcp.RegisterExpertTools(srv, cfg)
	agentmcp.RegisterArchitectTools(srv, cfg)

	initMsg := mustJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":   map[string]any{},
			"clientInfo":     map[string]any{"name": "test", "version": "0.1"},
		},
	})
	srv.HandleMessage(context.Background(), initMsg)

	// Call pool_send_task in a goroutine (it blocks on approval)
	resultCh := make(chan *mcp.CallToolResult, 1)
	go func() {
		r := callTool(t, srv, "send_task", map[string]any{
			"to":   "auth",
			"body": "implement token endpoint",
			"id":   "task-approval-001",
		})
		resultCh <- r
	}()

	// Wait for proposal file to appear
	proposalPath := filepath.Join(poolDir, "approvals", "task-approval-001.proposal.md")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(proposalPath); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if _, err := os.Stat(proposalPath); os.IsNotExist(err) {
		t.Fatal("expected proposal file to be written")
	}

	// Approve the proposal
	if err := approval.Respond(filepath.Join(poolDir, "approvals"), "task-approval-001", true, ""); err != nil {
		t.Fatalf("Respond: %v", err)
	}

	// Wait for result
	select {
	case result := <-resultCh:
		text := resultText(t, result)
		if !strings.Contains(text, "task-approval-001") {
			t.Errorf("expected task sent, got %q", text)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for send_task to complete after approval")
	}

	// Verify task reached postoffice
	if _, err := os.Stat(filepath.Join(poolDir, "postoffice", "task-approval-001.md")); err != nil {
		t.Error("task should be in postoffice after approval")
	}
}

func TestSendTask_ApprovalRejected(t *testing.T) {
	poolDir := setupArchitectPool(t)
	if err := os.MkdirAll(filepath.Join(poolDir, "approvals"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &agentmcp.ServerConfig{
		PoolDir:      poolDir,
		ExpertName:   "architect",
		Role:         "architect",
		ApprovalMode: "all",
		Logger:       slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	srv := server.NewMCPServer("agent-pool-test", "0.4.0-test")
	agentmcp.RegisterExpertTools(srv, cfg)
	agentmcp.RegisterArchitectTools(srv, cfg)

	initMsg := mustJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-03-26",
			"capabilities":   map[string]any{},
			"clientInfo":     map[string]any{"name": "test", "version": "0.1"},
		},
	})
	srv.HandleMessage(context.Background(), initMsg)

	resultCh := make(chan *mcp.CallToolResult, 1)
	go func() {
		r := callTool(t, srv, "send_task", map[string]any{
			"to":   "auth",
			"body": "implement auth",
			"id":   "task-rejected-001",
		})
		resultCh <- r
	}()

	// Wait for proposal
	proposalPath := filepath.Join(poolDir, "approvals", "task-rejected-001.proposal.md")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(proposalPath); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Reject the proposal with a reason
	if err := approval.Respond(filepath.Join(poolDir, "approvals"), "task-rejected-001", false, "needs more detail"); err != nil {
		t.Fatalf("Respond: %v", err)
	}

	select {
	case result := <-resultCh:
		if !result.IsError {
			t.Error("expected error result after rejection")
		}
		text := resultText(t, result)
		if !strings.Contains(text, "rejected") {
			t.Errorf("expected rejection in error, got %q", text)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for rejection response")
	}

	// Verify task did NOT reach postoffice
	if _, err := os.Stat(filepath.Join(poolDir, "postoffice", "task-rejected-001.md")); !os.IsNotExist(err) {
		t.Error("task should NOT be in postoffice after rejection")
	}
}
