package mcp

import (
	"github.com/cameronsjo/agent-pool/internal/mail"
)

// postMessage composes a mail message and writes it to the pool's postoffice.
// Delegates to mail.Post which handles directory creation and atomic writes.
func postMessage(poolDir string, msg *mail.Message) error {
	return mail.Post(poolDir, msg)
}
