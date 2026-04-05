package hooks

import (
	"fmt"
	"log/slog"
	"path/filepath"
)

// GuardConfig holds the parameters for the code ownership guard hook.
type GuardConfig struct {
	PoolDir    string
	ExpertName string
	FilePath   string
}

// Guard is the PreToolUse hook for code ownership enforcement.
// It checks whether an expert is allowed to modify a given file path.
//
// For v0.2, this is a soft guard: it logs the access but always allows.
// Future versions will add ownership mapping via [experts.auth.owns] config
// and enforce hard denies.
//
// Hook contract:
//   - Return nil (exit 0) → allow the tool call
//   - Return error (exit non-zero) → deny the tool call
func Guard(logger *slog.Logger, cfg *GuardConfig) error {
	if cfg == nil {
		return fmt.Errorf("guard config is nil")
	}
	if cfg.PoolDir == "" {
		return fmt.Errorf("pool directory is required")
	}
	if cfg.ExpertName == "" {
		return fmt.Errorf("expert name is required")
	}
	if cfg.ExpertName != filepath.Base(cfg.ExpertName) {
		return fmt.Errorf("invalid expert name %q: must not contain path separators", cfg.ExpertName)
	}

	logger.Info("Allowing file access",
		"expert", cfg.ExpertName,
		"path", cfg.FilePath,
	)

	return nil
}
