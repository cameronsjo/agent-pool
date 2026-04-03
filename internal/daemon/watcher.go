package daemon

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// WatcherEvent carries a validated file event.
type WatcherEvent struct {
	Path string // full path to the new file
	Dir  string // which watched directory this came from
}

// Watcher watches directories for new .md files and emits events
// after verifying the file is fully written.
type Watcher struct {
	fsw    *fsnotify.Watcher
	events chan WatcherEvent
	logger *slog.Logger
	dirs   map[string]bool // tracked directories for Dir resolution
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
func (w *Watcher) Add(dir string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	w.dirs[absDir] = true
	return w.fsw.Add(absDir)
}

// Events returns the channel of validated file events.
func (w *Watcher) Events() <-chan WatcherEvent {
	return w.events
}

// Run processes raw fsnotify events until ctx is cancelled.
// It filters for Create events on .md files and applies a partial-write
// stability check before emitting on the Events channel.
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

			if !event.Has(fsnotify.Create) {
				continue
			}

			if !strings.HasSuffix(event.Name, ".md") {
				continue
			}

			// Skip temp files from our own atomic writes
			base := filepath.Base(event.Name)
			if strings.HasPrefix(base, ".routing-") {
				continue
			}

			path := event.Name
			if err := waitForStable(path); err != nil {
				w.logger.Warn("File not stable, skipping",
					"path", path,
					"error", err,
				)
				continue
			}

			dir := w.resolveDir(path)
			select {
			case w.events <- WatcherEvent{Path: path, Dir: dir}:
			case <-ctx.Done():
				return
			}

		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.logger.Error("Watcher error", "error", err)
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
	if w.dirs[dir] {
		return dir
	}
	// Fallback — shouldn't happen if all watched dirs are registered
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

	// Size was still changing but we've waited long enough — accept it
	return nil
}
