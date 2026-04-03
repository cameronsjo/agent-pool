// Package mail handles parsing, routing, and delivery of agent-pool messages.
//
// Messages are markdown files with YAML frontmatter. The router reads the
// "to" header and copies messages from the postoffice to the target agent's inbox.
package mail
