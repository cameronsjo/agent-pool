// Package mcp implements the stdio MCP server that provides typed pool tools
// to expert sessions. Different tool sets are exposed per role (expert,
// architect, researcher, concierge).
//
// The server is invoked as: agent-pool mcp --pool <dir> --expert <name>
// Claude Code spawns it as a child process via --mcp-config.
package mcp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/mark3labs/mcp-go/server"
)

// ServerConfig holds the parameters for the MCP server instance.
type ServerConfig struct {
	PoolDir      string
	ExpertName   string // for experts: "auth"; for architect: "architect"
	Role         string // "expert" (default) or "architect"
	ApprovalMode string // architect only: "none", "decomposition", "all"
	Logger       *slog.Logger
}

// Validate checks that required fields are set.
func (c *ServerConfig) Validate() error {
	if c.PoolDir == "" {
		return fmt.Errorf("pool directory is required")
	}
	if c.ExpertName == "" {
		return fmt.Errorf("expert name is required")
	}
	return nil
}

// Run starts the stdio MCP server and blocks until stdin closes or a signal
// is received. Tool handlers read/write state files and mail in the pool
// directory. Logs go to stderr (stdout is the MCP transport).
func Run(ctx context.Context, cfg *ServerConfig) error {
	if cfg == nil {
		return fmt.Errorf("server config is nil")
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	srv := server.NewMCPServer(
		"agent-pool",
		"0.2.0",
	)

	RegisterExpertTools(srv, cfg)
	if cfg.Role == "architect" {
		RegisterArchitectTools(srv, cfg)
	}

	cfg.Logger.Info("Preparing to serve MCP tools",
		"pool_dir", cfg.PoolDir,
		"expert", cfg.ExpertName,
	)

	return server.ServeStdio(srv)
}
