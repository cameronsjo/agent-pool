package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"git.sjo.lol/cameron/agent-pool/internal/config"
	"git.sjo.lol/cameron/agent-pool/internal/daemon"
	"git.sjo.lol/cameron/agent-pool/internal/hooks"
	agentmcp "git.sjo.lol/cameron/agent-pool/internal/mcp"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "start":
		cmdStart()
	case "mcp":
		cmdMCP()
	case "flush":
		cmdFlush()
	case "guard":
		cmdGuard()
	case "version":
		fmt.Println("agent-pool v0.2.0-dev")
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func cmdStart() {
	poolDir := ""
	if len(os.Args) > 2 {
		poolDir = os.Args[2]
	}

	cfg, err := config.LoadPool(poolDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading pool config: %v\n", err)
		os.Exit(1)
	}

	// Resolve poolDir to absolute path for consistent path handling
	if poolDir == "" {
		poolDir, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error getting current directory: %v\n", err)
			os.Exit(1)
		}
	}
	poolDir, err = filepath.Abs(poolDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error resolving pool directory: %v\n", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	d := daemon.New(cfg, poolDir, logger)
	if err := d.Run(ctx); err != nil {
		logger.Error("Daemon failed", "error", err)
		os.Exit(1)
	}
}

// cmdMCP starts the stdio MCP server. Stdout is the MCP transport; logs go to stderr.
func cmdMCP() {
	flags := parseFlags(2, "pool", "expert")

	poolDir := flags["pool"]
	expertName := flags["expert"]

	if poolDir == "" || expertName == "" {
		fmt.Fprintf(os.Stderr, "usage: agent-pool mcp --pool <dir> --expert <name>\n")
		os.Exit(1)
	}

	cfg := &agentmcp.ServerConfig{
		PoolDir:    poolDir,
		ExpertName: expertName,
		Logger:     newStderrLogger(),
	}

	if err := agentmcp.Run(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "mcp server error: %v\n", err)
		os.Exit(1)
	}
}

func cmdFlush() {
	flags := parseFlags(2, "pool", "expert", "task")

	poolDir := flags["pool"]
	expertName := flags["expert"]
	taskID := flags["task"]

	if poolDir == "" || expertName == "" {
		fmt.Fprintf(os.Stderr, "usage: agent-pool flush --pool <dir> --expert <name> --task <id>\n")
		os.Exit(1)
	}

	cfg := &hooks.FlushConfig{
		PoolDir:    poolDir,
		ExpertName: expertName,
		TaskID:     taskID,
	}

	if err := hooks.Flush(newStderrLogger(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "flush error: %v\n", err)
		os.Exit(1)
	}
}

func cmdGuard() {
	flags := parseFlags(2, "pool", "expert", "path")

	poolDir := flags["pool"]
	expertName := flags["expert"]
	filePath := flags["path"]

	if poolDir == "" || expertName == "" {
		fmt.Fprintf(os.Stderr, "usage: agent-pool guard --pool <dir> --expert <name> --path <file>\n")
		os.Exit(1)
	}

	cfg := &hooks.GuardConfig{
		PoolDir:    poolDir,
		ExpertName: expertName,
		FilePath:   filePath,
	}

	if err := hooks.Guard(newStderrLogger(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "guard denied: %v\n", err)
		os.Exit(1)
	}
}

// newStderrLogger creates a JSON logger writing to stderr.
// Used by subcommands where stdout is reserved (MCP transport) or not needed.
func newStderrLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

// parseFlags extracts named --flag value pairs from os.Args[start:].
func parseFlags(start int, names ...string) map[string]string {
	return parseFlagsFromArgs(os.Args[start:], names...)
}

// parseFlagsFromArgs extracts named --flag value pairs from an args slice.
func parseFlagsFromArgs(args []string, names ...string) map[string]string {
	result := make(map[string]string, len(names))

	for i := 0; i < len(args)-1; i++ {
		for _, name := range names {
			if args[i] == "--"+name {
				result[name] = args[i+1]
				i++ // skip the value
				break
			}
		}
	}

	return result
}

func printUsage() {
	fmt.Println(`agent-pool — process supervisor for Claude Code expert sessions

Usage:
  agent-pool start [pool-dir]                          Start the daemon for a pool
  agent-pool mcp --pool <dir> --expert <name>          Start MCP server (stdio)
  agent-pool flush --pool <dir> --expert <name> --task <id>   Stop hook: verify state
  agent-pool guard --pool <dir> --expert <name> --path <file> PreToolUse hook: ownership guard
  agent-pool version                                   Print version
  agent-pool help                                      Show this help

Examples:
  agent-pool start ~/.agent-pool/pools/api-gateway
  agent-pool start              # uses current directory`)
}
