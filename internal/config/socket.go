package config

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
)

// ResolveSockPath returns the unix socket path for a pool directory.
// Defaults to {poolDir}/daemon.sock. Falls back to a hashed path under
// os.TempDir() when the default would exceed the macOS Unix socket path
// limit (104 bytes). Both the daemon and CLI must use this to agree on
// the socket location.
func ResolveSockPath(poolDir string) string {
	// Canonicalize so daemon and CLI agree on the path even when one
	// uses a relative path and the other uses an absolute one.
	if abs, err := filepath.Abs(poolDir); err == nil {
		poolDir = abs
	}
	candidate := filepath.Join(poolDir, "daemon.sock")
	if len(candidate) <= 100 {
		return candidate
	}
	h := fnv.New32a()
	h.Write([]byte(poolDir))
	return filepath.Join(os.TempDir(), fmt.Sprintf("ap-%x.sock", h.Sum32()))
}
