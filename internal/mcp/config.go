package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

// MCPConfig is the JSON structure claude expects for --mcp-config.
type MCPConfig struct {
	MCPServers map[string]MCPServerEntry `json:"mcpServers"`
}

// MCPServerEntry describes one MCP server for claude's --mcp-config.
type MCPServerEntry struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
	Type    string   `json:"type"`
}

// WriteTempConfig writes an MCP config JSON file to a temp directory and
// returns the path. The caller is responsible for removing the file when done.
//
// The config points claude at the current agent-pool binary as the MCP server,
// invoked as: agent-pool mcp --pool <poolDir> --expert <expertName>
func WriteTempConfig(poolDir, expertName string) (string, error) {
	binaryPath, err := resolveAgentPoolBinary()
	if err != nil {
		return "", fmt.Errorf("resolving agent-pool binary: %w", err)
	}

	cfg := MCPConfig{
		MCPServers: map[string]MCPServerEntry{
			"agent-pool": {
				Command: binaryPath,
				Args:    []string{"mcp", "--pool", poolDir, "--expert", expertName},
				Type:    "stdio",
			},
		},
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling MCP config: %w", err)
	}

	tmp, err := os.CreateTemp("", "agent-pool-mcp-*.json")
	if err != nil {
		return "", fmt.Errorf("creating temp file: %w", err)
	}

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", fmt.Errorf("writing temp file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("closing temp file: %w", err)
	}

	return tmp.Name(), nil
}

// resolveAgentPoolBinary finds the path to the current agent-pool binary.
// Prefers os.Executable (stable for built binaries), falls back to PATH lookup.
func resolveAgentPoolBinary() (string, error) {
	if path, err := os.Executable(); err == nil {
		return path, nil
	}
	if path, err := exec.LookPath("agent-pool"); err == nil {
		return path, nil
	}
	return "", fmt.Errorf("agent-pool binary not found")
}
