package mcp_test

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	agentmcp "git.sjo.lol/cameron/agent-pool/internal/mcp"
)

func TestServerConfig_Validate_HappyPath(t *testing.T) {
	cfg := &agentmcp.ServerConfig{
		PoolDir:    "/tmp/test-pool",
		ExpertName: "auth",
		Logger:     slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestServerConfig_Validate_EmptyPoolDir(t *testing.T) {
	cfg := &agentmcp.ServerConfig{
		ExpertName: "auth",
		Logger:     slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for empty pool dir")
	}
	if !strings.Contains(err.Error(), "pool directory") {
		t.Errorf("error = %q, want mention of 'pool directory'", err.Error())
	}
}

func TestServerConfig_Validate_EmptyExpertName(t *testing.T) {
	cfg := &agentmcp.ServerConfig{
		PoolDir: "/tmp/test-pool",
		Logger:  slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for empty expert name")
	}
	if !strings.Contains(err.Error(), "expert name") {
		t.Errorf("error = %q, want mention of 'expert name'", err.Error())
	}
}
