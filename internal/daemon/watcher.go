package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// EventKind distinguishes the type of file event.
type EventKind int

const (
	// EventKindMail is a new .md file (mail delivery).
	EventKindMail EventKind = iota
	// EventKindConfig is a .toml file write (config change).
	EventKindConfig
)

// WatcherEvent carries a validated file event.
type WatcherEvent struct {
	Path string    // full path to the new file
	Dir  string    // which watched directory this came from
	Kind EventKind // mail or config
}

// Watcher watches directories for new .md files and emits events
// after verifying the file is fully written.
type Watcher struct {
	fsw    *fsnotify.Watcher
	events chan WatcherEvent
	logger *slog.Logger

	mu   sync.Mutex
	dirs map[string]bool // tracked directories for Dir resolution
}

const (
	stabilityInterval = 100 * time.Millisecond
	stabilityAttempts = 5
	eventBufferSize   = 64
)

// NewWatcher creates a Watcher backed by fsnotify.
func NewWatcher(logger *slog.Logger) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Watcher{
		fsw:    fsw,
		events: make(chan WatcherEvent, eventBufferSize),
		logger: logger,
		dirs:   make(map[string]bool),
	}, nil
}

// Add registers a directory to watch. The directory must exist.
// Safe to call concurrently with Run (dirs map is guarded by w.mu).
func (w *Watcher) Add(dir string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	w.mu.Lock()
	w.dirs[absDir] = true
	w.mu.Unlock()
	return w.fsw.Add(absDir)
}

// Events returns the channel of validated file events.
func (w *Watcher) Events() <-chan WatcherEvent {
	return w.events
}

// Run processes raw fsnotify events until ctx is cancelled.
// It handles two kinds of events:
//   - Create events on .md files (mail delivery) → EventKindMail
//   - Write events on .toml files (config changes) → EventKindConfig
//
// Both kinds apply a partial-write stability check before emitting.
func (w *Watcher) Run(ctx context.Context) {
	defer close(w.events)

	for {
		select {
		case <-ctx.Done():
			return

		case event, ok := <-w.fsw.Events:
			if !ok {
				return
			}

			base := filepath.Base(event.Name)
			path := event.Name

			// Mail events: Create on .md files
			if event.Has(fsnotify.Create) && strings.HasSuffix(base, ".md") {
				// Skip temp files from our own atomic writes
				if strings.HasPrefix(base, ".routing-") {
					continue
				}

				if err := waitForStable(path); err != nil {
					w.logger.Warn("Skipping file. Reason: not stable after polling",
						"path", path,
						"error", err,
					)
					continue
				}

				dir := w.resolveDir(path)
				select {
				case w.events <- WatcherEvent{Path: path, Dir: dir, Kind: EventKindMail}:
				case <-ctx.Done():
					return
				}
				continue
			}

			// Config events: Write or Create on .toml files.
			// Write catches in-place edits. Create catches atomic
			// temp-file + rename updates (which emit Create, not Write).
			if (event.Has(fsnotify.Write) || event.Has(fsnotify.Create)) && strings.HasSuffix(base, ".toml") {
				if err := waitForStable(path); err != nil {
					w.logger.Warn("Skipping config file. Reason: not stable after polling",
						"path", path,
						"error", err,
					)
					continue
				}

				dir := w.resolveDir(path)
				select {
				case w.events <- WatcherEvent{Path: path, Dir: dir, Kind: EventKindConfig}:
				case <-ctx.Done():
					return
				}
				continue
			}

		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.logger.Error("Encountered watcher error", "error", err)
		}
	}
}

// Close shuts down the underlying fsnotify watcher.
func (w *Watcher) Close() error {
	return w.fsw.Close()
}

// resolveDir finds which watched directory a path belongs to.
func (w *Watcher) resolveDir(path string) string {
	absPath, _ := filepath.Abs(path)
	dir := filepath.Dir(absPath)
	w.mu.Lock()
	_ = w.dirs[dir] // lookup under lock for race safety
	w.mu.Unlock()
	return dir
}

// waitForStable polls a file until its size stabilizes, indicating the
// write is complete. Returns an error if the file disappears or never stabilizes.
func waitForStable(path string) error {
	var lastSize int64 = -1

	for i := range stabilityAttempts {
		time.Sleep(stabilityInterval)

		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) && i == 0 {
				continue // file may not be visible yet
			}
			return err
		}

		size := info.Size()
		if size == lastSize && size > 0 {
			return nil // size stabilized
		}
		lastSize = size
	}

	// If we got a stable size of 0 after all attempts, that's suspicious
	if lastSize == 0 {
		return os.ErrNotExist
	}

	return fmt.Errorf("file size still changing after %d attempts", stabilityAttempts)
}
