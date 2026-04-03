// Package hooks implements CLI hook commands for Claude Code's hook system.
//
// These are invoked by Claude Code during expert sessions:
//   - Stop hook: agent-pool flush --pool X --expert Y --task Z
//   - PreToolUse hook: agent-pool guard --pool X --expert Y --path Z
package hooks

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// FlushConfig holds the parameters for the flush hook.
type FlushConfig struct {
	PoolDir    string
	ExpertName string
	TaskID     string
}

// Flush is the Stop hook safety net. It verifies that state.md was updated
// during the session. If state.md is stale or missing, it logs a warning.
//
// This is a diagnostic hook — it does not fail the session. State writes
// happen via MCP tools during the session; this just catches the case where
// the expert forgot to update state.
func Flush(logger *slog.Logger, cfg *FlushConfig) error {
	if cfg.PoolDir == "" {
		return fmt.Errorf("pool directory is required")
	}
	if cfg.ExpertName == "" {
		return fmt.Errorf("expert name is required")
	}

	expertDir := filepath.Join(cfg.PoolDir, "experts", cfg.ExpertName)
	statePath := filepath.Join(expertDir, "state.md")

	info, err := os.Stat(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Warn("Expert has no state.md after session",
				"expert", cfg.ExpertName,
				"task_id", cfg.TaskID,
			)
			return nil
		}
		return fmt.Errorf("checking state.md: %w", err)
	}

	// Warn if state.md hasn't been modified in the last 30 minutes.
	// A healthy session updates state via pool_update_state MCP tool.
	staleThreshold := 30 * time.Minute
	if time.Since(info.ModTime()) > staleThreshold {
		logger.Warn("Expert state.md appears stale after session",
			"expert", cfg.ExpertName,
			"task_id", cfg.TaskID,
			"last_modified", info.ModTime().UTC().Format(time.RFC3339),
		)
	} else {
		logger.Info("Expert state verified after session",
			"expert", cfg.ExpertName,
			"task_id", cfg.TaskID,
		)
	}

	return nil
}
