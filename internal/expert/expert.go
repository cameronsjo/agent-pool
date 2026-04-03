// Package expert handles spawning, lifecycle management, and state assembly
// for headless Claude Code expert sessions.
//
// Each expert invocation is a fresh "claude -p" call with a prompt assembled
// from identity.md + state.md + errors.md + the task. The session is
// disposable; the knowledge persists on disk.
package expert
