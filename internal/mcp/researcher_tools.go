package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/cameronsjo/agent-pool/internal/atomicfile"
	"github.com/cameronsjo/agent-pool/internal/config"
	"github.com/cameronsjo/agent-pool/internal/expert"
	"github.com/cameronsjo/agent-pool/internal/mail"
)

// RegisterResearcherTools adds researcher-scope tools to the MCP server.
// These are registered in addition to the expert tools when running as
// the researcher role. The researcher reads cross-expert state and logs,
// writes curated state back, and promotes patterns to identity.
func RegisterResearcherTools(srv *server.MCPServer, cfg *ServerConfig) {
	if cfg == nil {
		return
	}

	srv.AddTool(
		mcp.NewTool("list_experts",
			mcp.WithDescription("List all experts in the pool with state file sizes, log counts, and last task time. Use to triage which experts need curation."),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
		),
		handleResearcherListExperts(cfg),
	)

	srv.AddTool(
		mcp.NewTool("read_expert_state",
			mcp.WithDescription("Read another expert's state files (identity.md, state.md, errors.md). Returns all files by default, or a specific file."),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
			mcp.WithString("expert", mcp.Required(), mcp.Description("Expert name to read state from")),
			mcp.WithString("file", mcp.Description("Specific file to read: 'identity', 'state', 'errors', or 'all' (default)")),
		),
		handleReadExpertState(cfg),
	)

	srv.AddTool(
		mcp.NewTool("read_expert_logs",
			mcp.WithDescription("Read another expert's recent log index entries. Returns the last N entries, optionally filtered by a search query."),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
			mcp.WithString("expert", mcp.Required(), mcp.Description("Expert name to read logs from")),
			mcp.WithString("count", mcp.Description("Number of recent entries to return (default: 10)")),
			mcp.WithString("query", mcp.Description("Optional search query to filter entries (case-insensitive substring)")),
		),
		handleReadExpertLogs(cfg),
	)

	srv.AddTool(
		mcp.NewTool("enrich_state",
			mcp.WithDescription("Assemble an expert's full context for curation analysis. Returns identity, state, errors, recent log index entries, and the last 3 full log file contents."),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{ReadOnlyHint: boolPtr(true)}),
			mcp.WithString("expert", mcp.Required(), mcp.Description("Expert name to assemble context for")),
		),
		handleEnrichState(cfg),
	)

	srv.AddTool(
		mcp.NewTool("write_expert_state",
			mcp.WithDescription("Write curated state back to an expert. Targets state.md by default. Content must be non-empty and under 50KB. For shared experts, use layer to target user-level or project-level state."),
			mcp.WithString("expert", mcp.Required(), mcp.Description("Expert name to write state to")),
			mcp.WithString("content", mcp.Required(), mcp.Description("New file content")),
			mcp.WithString("file", mcp.Description("Target file: 'state' (default) or 'errors'")),
			mcp.WithString("layer", mcp.Description("For shared experts: 'user' (default) or 'project'. Ignored for pool-scoped experts.")),
		),
		handleWriteExpertState(cfg),
	)

	srv.AddTool(
		mcp.NewTool("promote_pattern",
			mcp.WithDescription("Append a graduated pattern to an expert's identity.md. Patterns promoted to identity become permanent expert knowledge."),
			mcp.WithString("expert", mcp.Required(), mcp.Description("Expert name")),
			mcp.WithString("pattern", mcp.Required(), mcp.Description("Pattern text to append (markdown)")),
			mcp.WithString("section", mcp.Description("Heading to append under (default: '## Graduated Patterns')")),
		),
		handlePromotePattern(cfg),
	)
}

// expertInfo holds metadata about an expert for list_experts.
type expertInfo struct {
	Name       string `json:"name"`
	Type       string `json:"type"` // "pool" or "shared"
	StateBytes int64  `json:"state_bytes"`
	LogCount   int    `json:"log_count"`
	LastTask   string `json:"last_task,omitempty"`
}

func handleResearcherListExperts(cfg *ServerConfig) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		poolCfg, err := config.LoadPool(cfg.PoolDir)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("loading pool config: %v", err)), nil
		}

		var experts []expertInfo

		// Pool-scoped experts
		var names []string
		for name := range poolCfg.Experts {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			dir := mail.ResolveExpertDir(cfg.PoolDir, name)
			experts = append(experts, gatherExpertInfo(name, "pool", dir, dir))
		}

		// Shared experts — state in user dir, logs in pool overlay
		for _, name := range poolCfg.Shared.Include {
			dir, err := config.SharedExpertDir(name)
			if err != nil {
				continue
			}
			overlayDir := filepath.Join(cfg.PoolDir, "shared-state", name)
			experts = append(experts, gatherExpertInfo(name, "shared", dir, overlayDir))
		}

		data, err := json.MarshalIndent(experts, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshaling result: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

// gatherExpertInfo stats an expert directory for metadata. The logBaseDir
// parameter specifies where logs live (same as dir for pool-scoped experts,
// but the pool overlay for shared experts).
func gatherExpertInfo(name, expertType, dir, logBaseDir string) expertInfo {
	info := expertInfo{Name: name, Type: expertType}

	if fi, err := os.Stat(filepath.Join(dir, "state.md")); err == nil {
		info.StateBytes = fi.Size()
	}

	logsDir := filepath.Join(logBaseDir, "logs")
	if entries, err := os.ReadDir(logsDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				info.LogCount++
			}
		}
	}

	// Last task from index.md (last non-empty line)
	if data, err := os.ReadFile(filepath.Join(logsDir, "index.md")); err == nil {
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		for i := len(lines) - 1; i >= 2; i-- { // skip header rows
			line := strings.TrimSpace(lines[i])
			if line != "" && strings.HasPrefix(line, "|") {
				// Extract task ID from first column
				parts := strings.SplitN(line, "|", 3)
				if len(parts) >= 3 {
					info.LastTask = strings.TrimSpace(parts[1])
				}
				break
			}
		}
	}

	return info
}

func handleReadExpertState(cfg *ServerConfig) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		expertName := request.GetString("expert", "")
		if expertName == "" {
			return mcp.NewToolResultError("expert parameter is required"), nil
		}

		dir := resolveTargetExpertDir(cfg.PoolDir, expertName)
		file := request.GetString("file", "all")

		switch file {
		case "identity":
			content, err := readFileOr(filepath.Join(dir, "identity.md"), "")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("reading identity.md: %v", err)), nil
			}
			return mcp.NewToolResultText(content), nil

		case "state":
			content, err := readFileOr(filepath.Join(dir, "state.md"), "")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("reading state.md: %v", err)), nil
			}
			return mcp.NewToolResultText(content), nil

		case "errors":
			content, err := readFileOr(filepath.Join(dir, "errors.md"), "")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("reading errors.md: %v", err)), nil
			}
			return mcp.NewToolResultText(content), nil

		case "all", "":
			identity, state, errors, err := expert.ReadState(dir)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("reading state: %v", err)), nil
			}
			result := map[string]string{
				"identity": identity,
				"state":    state,
				"errors":   errors,
			}
			// For shared experts, include the project overlay state
			overlayDir := resolveSharedOverlayDir(cfg.PoolDir, expertName)
			if overlayDir != "" {
				overlayState := readOverlayState(overlayDir)
				if overlayState != "" {
					result["project_state"] = overlayState
				}
			}
			data, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("marshaling: %v", err)), nil
			}
			return mcp.NewToolResultText(string(data)), nil

		default:
			return mcp.NewToolResultError(fmt.Sprintf("invalid file %q: use 'identity', 'state', 'errors', or 'all'", file)), nil
		}
	}
}

func handleReadExpertLogs(cfg *ServerConfig) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		expertName := request.GetString("expert", "")
		if expertName == "" {
			return mcp.NewToolResultError("expert parameter is required"), nil
		}

		// Logs dir may differ from state dir for shared experts
		dir := resolveLogsDir(cfg.PoolDir, expertName)
		query := request.GetString("query", "")

		countStr := request.GetString("count", "10")
		count := 10
		if _, err := fmt.Sscanf(countStr, "%d", &count); err != nil || count <= 0 {
			count = 10
		}

		if query != "" {
			matches, err := expert.SearchIndex(dir, query)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("searching index: %v", err)), nil
			}
			if len(matches) == 0 {
				return mcp.NewToolResultText("no matching entries found"), nil
			}
			if len(matches) > count {
				matches = matches[len(matches)-count:]
			}
			return mcp.NewToolResultText(strings.Join(matches, "\n")), nil
		}

		// No query — return last N entries from index
		indexPath := filepath.Join(dir, "logs", "index.md")
		data, err := os.ReadFile(indexPath)
		if err != nil {
			if os.IsNotExist(err) {
				return mcp.NewToolResultText("no log entries yet"), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("reading index: %v", err)), nil
		}

		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		// Skip header rows (lines 0 and 1)
		var entries []string
		for i := 2; i < len(lines); i++ {
			line := strings.TrimSpace(lines[i])
			if line != "" {
				entries = append(entries, line)
			}
		}

		if len(entries) == 0 {
			return mcp.NewToolResultText("no log entries yet"), nil
		}

		// Return last N
		start := 0
		if len(entries) > count {
			start = len(entries) - count
		}
		return mcp.NewToolResultText(strings.Join(entries[start:], "\n")), nil
	}
}

func handleEnrichState(cfg *ServerConfig) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		expertName := request.GetString("expert", "")
		if expertName == "" {
			return mcp.NewToolResultError("expert parameter is required"), nil
		}

		stateDir := resolveTargetExpertDir(cfg.PoolDir, expertName)
		logDir := resolveLogsDir(cfg.PoolDir, expertName)

		identity, state, errors, err := expert.ReadState(stateDir)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("reading state: %v", err)), nil
		}

		// Read last 10 index entries
		var recentIndex []string
		indexPath := filepath.Join(logDir, "logs", "index.md")
		if data, err := os.ReadFile(indexPath); err == nil {
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			for i := 2; i < len(lines); i++ {
				line := strings.TrimSpace(lines[i])
				if line != "" {
					recentIndex = append(recentIndex, line)
				}
			}
			if len(recentIndex) > 10 {
				recentIndex = recentIndex[len(recentIndex)-10:]
			}
		}

		// Read last 3 full log files (newest first by filename sort)
		var recentLogs []map[string]string
		logsDir := filepath.Join(logDir, "logs")
		if entries, err := os.ReadDir(logsDir); err == nil {
			var jsonFiles []string
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
					jsonFiles = append(jsonFiles, e.Name())
				}
			}
			sort.Strings(jsonFiles) // lexicographic ≈ chronological for task IDs
			if len(jsonFiles) > 3 {
				jsonFiles = jsonFiles[len(jsonFiles)-3:]
			}
			for _, f := range jsonFiles {
				content, readErr := os.ReadFile(filepath.Join(logsDir, f))
				if readErr == nil {
					taskID := strings.TrimSuffix(f, ".json")
					summary := expert.ExtractSummary(content)
					recentLogs = append(recentLogs, map[string]string{
						"task_id": taskID,
						"summary": summary,
					})
				}
			}
		}

		result := map[string]any{
			"identity":     identity,
			"state":        state,
			"errors":       errors,
			"recent_index": recentIndex,
			"recent_logs":  recentLogs,
		}

		// For shared experts, include project overlay state
		overlayDir := resolveSharedOverlayDir(cfg.PoolDir, expertName)
		if overlayDir != "" {
			overlayState := readOverlayState(overlayDir)
			if overlayState != "" {
				result["project_state"] = overlayState
			}
		}

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("marshaling: %v", err)), nil
		}

		return mcp.NewToolResultText(string(data)), nil
	}
}

func handleWriteExpertState(cfg *ServerConfig) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		expertName := request.GetString("expert", "")
		if expertName == "" {
			return mcp.NewToolResultError("expert parameter is required"), nil
		}

		content := request.GetString("content", "")
		if content == "" {
			return mcp.NewToolResultError("content parameter is required"), nil
		}

		file := request.GetString("file", "state")
		layer := request.GetString("layer", "user")

		// Resolve write directory: shared experts route by layer
		dir := resolveTargetExpertDir(cfg.PoolDir, expertName)
		overlayDir := resolveSharedOverlayDir(cfg.PoolDir, expertName)
		if overlayDir != "" && layer == "project" && file == "state" {
			dir = overlayDir
			// Ensure overlay dir exists
			os.MkdirAll(dir, 0o755)
		}

		switch file {
		case "state", "":
			if err := expert.WriteState(dir, content); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("writing state.md: %v", err)), nil
			}
			layerLabel := ""
			if overlayDir != "" {
				layerLabel = fmt.Sprintf(" (layer: %s)", layer)
			}
			return mcp.NewToolResultText(fmt.Sprintf("%s/state.md updated (%d bytes)%s", expertName, len(content), layerLabel)), nil

		case "errors":
			// Overwrite errors.md entirely (not append — researcher is curating)
			content = strings.TrimSpace(content)
			if len(content) > expert.MaxStateSize {
				return mcp.NewToolResultError(fmt.Sprintf("content exceeds maximum size (%d > %d bytes)", len(content), expert.MaxStateSize)), nil
			}
			path := filepath.Join(dir, "errors.md")
			if err := atomicfile.WriteFile(path, []byte(content+"\n")); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("writing errors.md: %v", err)), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("%s/errors.md updated (%d bytes)", expertName, len(content))), nil

		default:
			return mcp.NewToolResultError(fmt.Sprintf("invalid file %q: use 'state' or 'errors'", file)), nil
		}
	}
}

func handlePromotePattern(cfg *ServerConfig) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		expertName := request.GetString("expert", "")
		if expertName == "" {
			return mcp.NewToolResultError("expert parameter is required"), nil
		}

		pattern := request.GetString("pattern", "")
		if pattern == "" {
			return mcp.NewToolResultError("pattern parameter is required"), nil
		}

		section := request.GetString("section", "## Graduated Patterns")

		dir := resolveTargetExpertDir(cfg.PoolDir, expertName)
		identityPath := filepath.Join(dir, "identity.md")

		existing, err := os.ReadFile(identityPath)
		if err != nil && !os.IsNotExist(err) {
			return mcp.NewToolResultError(fmt.Sprintf("reading identity.md: %v", err)), nil
		}

		content := string(existing)

		// Find or create the target section
		if strings.Contains(content, section) {
			// Append after the section heading
			idx := strings.Index(content, section)
			insertAt := idx + len(section)
			// Skip to end of heading line
			if nl := strings.Index(content[insertAt:], "\n"); nl >= 0 {
				insertAt += nl
			} else {
				insertAt = len(content)
			}
			content = content[:insertAt] + "\n\n" + strings.TrimSpace(pattern) + "\n" + content[insertAt:]
		} else {
			// Append section at end
			if content != "" && !strings.HasSuffix(content, "\n") {
				content += "\n"
			}
			content += "\n" + section + "\n\n" + strings.TrimSpace(pattern) + "\n"
		}

		if err := atomicfile.WriteFile(identityPath, []byte(content)); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("writing identity.md: %v", err)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf("pattern promoted to %s/identity.md under %q", expertName, section)), nil
	}
}

// resolveTargetExpertDir returns the state directory for a target expert.
// Built-in roles use {poolDir}/{role}/, pool-scoped use {poolDir}/experts/{name}/.
// For shared experts, returns the user-level directory (identity + state).
func resolveTargetExpertDir(poolDir, name string) string {
	if isSharedExpert(poolDir, name) {
		dir, err := config.SharedExpertDir(name)
		if err == nil {
			return dir
		}
	}
	return mail.ResolveExpertDir(poolDir, name)
}

// resolveLogsDir returns the directory containing logs for an expert.
// For shared experts, logs live in the pool overlay (shared-state/<name>/).
// For pool-scoped experts, logs are in the expert dir itself.
func resolveLogsDir(poolDir, name string) string {
	if isSharedExpert(poolDir, name) {
		return filepath.Join(poolDir, "shared-state", name)
	}
	return mail.ResolveExpertDir(poolDir, name)
}

// resolveSharedOverlayDir returns the pool-scoped overlay directory for a shared expert.
// Returns empty string if the expert is not shared.
func resolveSharedOverlayDir(poolDir, name string) string {
	if !isSharedExpert(poolDir, name) {
		return ""
	}
	return filepath.Join(poolDir, "shared-state", name)
}

// isSharedExpert checks whether an expert is in the pool's shared.include list.
func isSharedExpert(poolDir, name string) bool {
	poolCfg, err := config.LoadPool(poolDir)
	if err != nil {
		return false
	}
	for _, n := range poolCfg.Shared.Include {
		if n == name {
			return true
		}
	}
	return false
}

// readFileOr reads a file and returns its trimmed content, or the fallback if not found.
func readFileOr(path, fallback string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fallback, nil
		}
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
