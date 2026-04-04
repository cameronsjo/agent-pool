package mail

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// builtinRoles are roles with top-level directories (not under experts/).
var builtinRoles = map[string]bool{
	"architect":  true,
	"researcher": true,
	"concierge":  true,
}

// IsBuiltinRole reports whether the given name is a built-in role (architect,
// researcher, concierge) rather than a pool-scoped expert.
func IsBuiltinRole(name string) bool {
	return builtinRoles[name]
}

// ResolveExpertDir returns the state directory for an expert or built-in role.
// Built-in roles use {poolDir}/{role}/, experts use {poolDir}/experts/{name}/.
func ResolveExpertDir(poolDir, name string) string {
	if builtinRoles[name] {
		return filepath.Join(poolDir, name)
	}
	return filepath.Join(poolDir, "experts", name)
}

// ResolveInbox returns the inbox directory path for a recipient within a pool.
//
// Built-in roles (architect, researcher, concierge):
//
//	{poolDir}/{role}/inbox/
//
// Experts:
//
//	{poolDir}/experts/{name}/inbox/
func ResolveInbox(poolDir, recipient string) string {
	if builtinRoles[recipient] {
		return filepath.Join(poolDir, recipient, "inbox")
	}
	return filepath.Join(poolDir, "experts", recipient, "inbox")
}

// Route parses a message from the postoffice, copies it to the recipient's
// inbox, and deletes the original. Delivery is at-least-once: the copy
// completes before the delete.
func Route(logger *slog.Logger, poolDir, filePath string) (*Message, error) {
	msg, err := ParseFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("parsing message for routing: %w", err)
	}

	inboxDir := ResolveInbox(poolDir, msg.To)

	if _, err := os.Stat(inboxDir); err != nil {
		return nil, fmt.Errorf("inbox not available for recipient %q: %w", msg.To, err)
	}

	// Use message ID as filename to avoid collisions on duplicate delivery
	destPath := filepath.Join(inboxDir, msg.ID+".md")

	if err := copyFile(filePath, destPath); err != nil {
		return nil, fmt.Errorf("copying to inbox: %w", err)
	}

	if err := os.Remove(filePath); err != nil {
		// Copy succeeded but delete failed — at-least-once is preserved.
		// Log the error but don't fail the route.
		logger.Warn("Failed to delete routed message from postoffice",
			"path", filePath,
			"error", err,
		)
	}

	logger.Info("Successfully routed message",
		"id", msg.ID,
		"from", msg.From,
		"to", msg.To,
		"type", msg.Type,
	)

	return msg, nil
}

// copyFile copies src to dst with an atomic write pattern (write to temp, rename).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening source: %w", err)
	}
	defer in.Close()

	// Write to a temp file in the same directory, then rename for atomicity
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, ".routing-*.md")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("copying content: %w", err)
	}

	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Rename(tmpPath, dst); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming to destination: %w", err)
	}

	return nil
}
