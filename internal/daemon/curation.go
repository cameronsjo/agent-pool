package daemon

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cameronsjo/agent-pool/internal/config"
	"github.com/cameronsjo/agent-pool/internal/mail"
)

// curationScheduler tracks task completions and triggers researcher curation
// when thresholds are reached (task count or time elapsed).
type curationScheduler struct {
	intervalTasks int
	intervalHours int
	poolDir       string
	logger        *slog.Logger

	mu           sync.Mutex
	taskCount    int
	lastCuration time.Time
}

func newCurationScheduler(cfg *config.CurationSection, poolDir string, logger *slog.Logger) *curationScheduler {
	return &curationScheduler{
		intervalTasks: cfg.IntervalTasks,
		intervalHours: cfg.IntervalHours,
		poolDir:       poolDir,
		logger:        logger,
		lastCuration:  time.Now(),
	}
}

// RecordTaskCompletion increments the completed task counter. Returns true
// when the threshold is reached, signaling that curation should be triggered.
func (cs *curationScheduler) RecordTaskCompletion() bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.taskCount++
	return cs.taskCount >= cs.intervalTasks
}

// ShouldTriggerByTime returns true if enough time has elapsed since the last
// curation trigger.
func (cs *curationScheduler) ShouldTriggerByTime() bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	return time.Since(cs.lastCuration) >= time.Duration(cs.intervalHours)*time.Hour
}

// Reset zeroes the task counter and updates the last curation timestamp.
func (cs *curationScheduler) Reset() {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.taskCount = 0
	cs.lastCuration = time.Now()
}

// triggerCuration composes a curation task and posts it to the researcher's
// inbox via the postoffice. The task body includes expert names and their
// state sizes to guide the researcher's curation decisions.
func (d *Daemon) triggerCuration(reason string) {
	d.logger.Info("Triggering curation",
		"reason", reason,
	)

	body := buildCurationTaskBody(d.cfg, d.poolDir, reason)

	msg := &mail.Message{
		ID:        fmt.Sprintf("curation-%d", time.Now().UnixMilli()),
		From:      "daemon",
		To:        "researcher",
		Type:      mail.TypeTask,
		Priority:  mail.PriorityNormal,
		Timestamp: time.Now().UTC(),
		Body:      body,
	}

	if err := mail.Post(d.poolDir, msg); err != nil {
		d.logger.Error("Failed to post curation task",
			"error", err,
		)
		return
	}

	d.events.emit(Event{
		Type:      EventCurationTriggered,
		Timestamp: time.Now(),
		Data:      CurationTriggeredData{Reason: reason},
	})
}

// buildCurationTaskBody assembles the structured task body for a curation
// request. Includes per-expert metadata to help the researcher prioritize.
func buildCurationTaskBody(cfg *config.PoolConfig, poolDir, reason string) string {
	var b strings.Builder

	b.WriteString("## Curation Task\n\n")
	b.WriteString(fmt.Sprintf("**Trigger:** %s\n\n", reason))
	b.WriteString("Review each expert's state and logs. Prune stale information from state.md, ")
	b.WriteString("promote recurring patterns to identity.md, and check state sizes.\n\n")
	b.WriteString("### Experts to Curate\n\n")
	b.WriteString("| Expert | Type | State Size | Log Count |\n")
	b.WriteString("|--------|------|------------|----------:|\n")

	// Pool-scoped experts
	var names []string
	for name := range cfg.Experts {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		dir := mail.ResolveExpertDir(poolDir, name)
		stateSize := fileSize(filepath.Join(dir, "state.md"))
		logCount := countLogFiles(filepath.Join(dir, "logs"))
		b.WriteString(fmt.Sprintf("| %s | pool | %d bytes | %d |\n", name, stateSize, logCount))
	}

	// Shared experts
	for _, name := range cfg.Shared.Include {
		dir, err := config.SharedExpertDir(name)
		if err != nil {
			continue
		}
		stateSize := fileSize(filepath.Join(dir, "state.md"))
		logCount := countLogFiles(filepath.Join(dir, "logs"))
		b.WriteString(fmt.Sprintf("| %s | shared | %d bytes | %d |\n", name, stateSize, logCount))
	}

	b.WriteString("\nUse `enrich_state` to read each expert's full context, then ")
	b.WriteString("`write_expert_state` to write curated state back. Use `promote_pattern` ")
	b.WriteString("for patterns that should become permanent identity.\n")

	return b.String()
}

func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

func countLogFiles(logsDir string) int {
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			count++
		}
	}
	return count
}
