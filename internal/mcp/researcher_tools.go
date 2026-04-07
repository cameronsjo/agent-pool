package mcp

import (
	"github.com/mark3labs/mcp-go/server"
)

// RegisterResearcherTools adds researcher-scope tools to the MCP server.
// These are registered in addition to the expert tools when running as
// the researcher role. The researcher reads cross-expert state and logs,
// writes curated state back, and promotes patterns to identity.
func RegisterResearcherTools(srv *server.MCPServer, cfg *ServerConfig) {
	if cfg == nil {
		return
	}

	// Phase 2 will add: list_experts, read_expert_state, read_expert_logs,
	// enrich_state, write_expert_state, promote_pattern
}
