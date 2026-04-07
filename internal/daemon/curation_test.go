// Test plan for curation.go:
//
// Daemon integration:
//   [x] Curation triggered after N task completions — researcher spawned
//   [x] Curation task body contains expert metadata

package daemon_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cameronsjo/agent-pool/internal/config"
	"github.com/cameronsjo/agent-pool/internal/daemon"
	"github.com/cameronsjo/agent-pool/internal/taskboard"
)

func TestCurationScheduler_TaskThreshold(t *testing.T) {
	poolDir := t.TempDir()
	poolToml := `[pool]
name = "curation-test"
project_dir = "` + poolDir + `"

[curation]
interval_tasks = 3
interval_hours = 168

[experts.auth]
`
	os.WriteFile(filepath.Join(poolDir, "pool.toml"), []byte(poolToml), 0o644)

	cfg, err := config.LoadPool(poolDir)
	if err != nil {
		t.Fatalf("LoadPool: %v", err)
	}

	fake := &fakeSpawner{}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	d := daemon.New(cfg, poolDir, logger, daemon.WithSpawner(fake))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()

	time.Sleep(500 * time.Millisecond)

	// Send 3 tasks to auth (threshold is 3)
	for i := 1; i <= 3; i++ {
		id := "task-cur-00" + string(rune('0'+i))
		writeMessage(t, filepath.Join(poolDir, "postoffice"), id, "architect", "auth")
		time.Sleep(1 * time.Second)
	}

	// Wait for the researcher to be spawned (curation task triggers researcher)
	deadline := time.Now().Add(10 * time.Second)
	var researcherSpawned bool
	for time.Now().Before(deadline) {
		for _, c := range fake.getCalls() {
			if c.Name == "researcher" && strings.HasPrefix(c.TaskMessage.ID, "curation-") {
				researcherSpawned = true
				break
			}
		}
		if researcherSpawned {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	if !researcherSpawned {
		calls := fake.getCalls()
		var names []string
		for _, c := range calls {
			names = append(names, c.Name+":"+c.TaskMessage.ID)
		}
		t.Fatalf("expected researcher spawn with curation task, got spawns: %v", names)
	}

	// Verify the curation task body contains expert metadata
	var curationTaskID string
	for _, c := range fake.getCalls() {
		if c.Name == "researcher" && strings.HasPrefix(c.TaskMessage.ID, "curation-") {
			curationTaskID = c.TaskMessage.ID
			body := c.TaskMessage.Body
			if !strings.Contains(body, "auth") {
				t.Error("curation task body should mention auth expert")
			}
			if !strings.Contains(body, "task_threshold") {
				t.Error("curation task body should contain trigger reason")
			}
			if !strings.Contains(body, "enrich_state") {
				t.Error("curation task body should reference enrich_state tool")
			}
			break
		}
	}

	// Wait for curation task to complete before shutdown
	if curationTaskID != "" {
		waitForTaskStatus(t, poolDir, curationTaskID, taskboard.StatusCompleted)
	}
	shutdownDaemon(t, cancel, errCh)
}
