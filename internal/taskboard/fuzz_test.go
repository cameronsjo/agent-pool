// Fuzz harnesses for taskboard JSON loading.
//
// Candidates:
//   taskboard.Load (I/O boundary — unmarshals JSON from daemon-managed file)
//     [x] No-crash: arbitrary JSON must not panic
//
// Run:
//   go test -fuzz=FuzzLoadJSON -fuzztime=30s ./internal/taskboard/
package taskboard

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzLoadJSON writes arbitrary bytes to a file and loads it as a taskboard.
// Must not panic for any input.
func FuzzLoadJSON(f *testing.F) {
	// Valid taskboard
	f.Add([]byte(`{"version":1,"tasks":{"t1":{"id":"t1","status":"pending","expert":"auth","from":"architect","type":"task","priority":"normal","created_at":"2026-04-03T12:00:00Z","handoff_count":0}}}`))

	// Empty/minimal
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"version":1,"tasks":{}}`))
	f.Add([]byte(`{"version":1,"tasks":null}`))

	// Invalid JSON
	f.Add([]byte(`not json`))
	f.Add([]byte(`{`))
	f.Add([]byte(``))
	f.Add([]byte(`null`))
	f.Add([]byte(`[]`))

	// Type confusion
	f.Add([]byte(`{"version":"one","tasks":"not a map"}`))
	f.Add([]byte(`{"version":999999999,"tasks":{"t":null}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		path := filepath.Join(dir, "taskboard.json")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return
		}
		// Must not panic. Errors are expected for malformed input.
		_, _ = Load(path)
	})
}
