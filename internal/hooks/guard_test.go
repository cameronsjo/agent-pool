// Test plan for guard.go:
//
// Guard:
//   - Always returns nil (soft guard in v0.2)
//   - Empty pool dir → error
//   - Empty expert name → error

package hooks_test

import (
	"strings"
	"testing"

	"github.com/cameronsjo/agent-pool/internal/hooks"
)

func TestGuard_AllowsAll(t *testing.T) {
	cfg := &hooks.GuardConfig{
		PoolDir:    "/tmp/test-pool",
		ExpertName: "auth",
		FilePath:   "/src/auth/handler.go",
	}

	err := hooks.Guard(testLogger(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGuard_EmptyPoolDir(t *testing.T) {
	cfg := &hooks.GuardConfig{
		ExpertName: "auth",
		FilePath:   "/src/auth/handler.go",
	}

	err := hooks.Guard(testLogger(), cfg)
	if err == nil {
		t.Fatal("expected error for empty pool dir")
	}
	if !strings.Contains(err.Error(), "pool directory") {
		t.Errorf("error = %q, want mention of 'pool directory'", err.Error())
	}
}

func TestGuard_EmptyExpertName(t *testing.T) {
	cfg := &hooks.GuardConfig{
		PoolDir:  "/tmp",
		FilePath: "/src/auth/handler.go",
	}

	err := hooks.Guard(testLogger(), cfg)
	if err == nil {
		t.Fatal("expected error for empty expert name")
	}
}
