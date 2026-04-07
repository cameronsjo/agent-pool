package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cameronsjo/agent-pool/internal/config"
	"github.com/cameronsjo/agent-pool/internal/daemon"
	"github.com/cameronsjo/agent-pool/internal/hooks"
	"github.com/cameronsjo/agent-pool/internal/mail"
	agentmcp "github.com/cameronsjo/agent-pool/internal/mcp"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "start":
		cmdStart()
	case "stop":
		cmdStop()
	case "status":
		cmdStatus()
	case "watch":
		cmdWatch()
	case "mcp":
		cmdMCP()
	case "flush":
		cmdFlush()
	case "guard":
		cmdGuard()
	case "seed":
		cmdSeed()
	case "version":
		fmt.Println("agent-pool v0.6.0-dev")
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func cmdStart() {
	explicit := ""
	if len(os.Args) > 2 {
		explicit = os.Args[2]
	}

	poolDir, err := config.DiscoverPoolDir(explicit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	poolDir, err = filepath.Abs(poolDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error resolving pool directory: %v\n", err)
		os.Exit(1)
	}

	cfg, err := config.LoadPool(poolDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading pool config: %v\n", err)
		os.Exit(1)
	}

	// Log to {poolDir}/daemon.log by default, tee to stdout as well
	logPath := filepath.Join(poolDir, "daemon.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening log file %s: %v\n", logPath, err)
		os.Exit(1)
	}
	defer logFile.Close()

	logWriter := io.MultiWriter(os.Stdout, logFile)
	logger := slog.New(slog.NewJSONHandler(logWriter, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	logger.Info("Logging to file", "path", logPath)

	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigCh)
	defer cancel()

	// Double-signal: first signal triggers graceful drain, second forces exit.
	go func() {
		<-sigCh
		cancel()
		<-sigCh
		logger.Warn("Received second signal, forcing immediate exit")
		os.Exit(1)
	}()

	d := daemon.New(cfg, poolDir, logger)
	if err := d.Run(ctx); err != nil {
		logger.Error("Daemon failed", "error", err)
		os.Exit(1)
	}
}

func cmdStop() {
	explicit := ""
	if len(os.Args) > 2 {
		explicit = os.Args[2]
	}

	poolDir, err := config.DiscoverPoolDir(explicit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	resp, err := connectAndSend(config.ResolveSockPath(poolDir), "stop")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if resp.Status != "ok" {
		fmt.Fprintf(os.Stderr, "error: %s\n", resp.Message)
		os.Exit(1)
	}

	fmt.Println("Daemon is shutting down.")
}

func cmdStatus() {
	explicit := ""
	if len(os.Args) > 2 {
		explicit = os.Args[2]
	}

	poolDir, err := config.DiscoverPoolDir(explicit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	resp, err := connectAndSend(config.ResolveSockPath(poolDir), "status")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if resp.Status != "ok" {
		fmt.Fprintf(os.Stderr, "error: %s\n", resp.Message)
		os.Exit(1)
	}

	var data map[string]json.RawMessage
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		// Fallback to raw JSON
		fmt.Println(string(resp.Data))
		return
	}

	printStatusField := func(label, key string) {
		if v, ok := data[key]; ok {
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				fmt.Printf("%-10s %s\n", label+":", string(v))
				return
			}
			fmt.Printf("%-10s %s\n", label+":", s)
		}
	}

	printStatusField("Pool", "pool")
	printStatusField("State", "state")
	printStatusField("Uptime", "uptime")

	// Experts
	if v, ok := data["experts"]; ok {
		var experts []string
		json.Unmarshal(v, &experts)
		fmt.Printf("%-10s %s\n", "Experts:", strings.Join(experts, ", "))
	}

	// Task counts
	if v, ok := data["task_counts"]; ok {
		var counts map[string]int
		json.Unmarshal(v, &counts)
		if len(counts) > 0 {
			fmt.Println("\nTasks:")
			for _, status := range []string{"pending", "blocked", "active", "completed", "failed", "cancelled"} {
				if n, ok := counts[status]; ok && n > 0 {
					fmt.Printf("  %-12s %d\n", status+":", n)
				}
			}
		}
	}

	// Active tasks
	if v, ok := data["active_tasks"]; ok {
		var tasks []map[string]string
		json.Unmarshal(v, &tasks)
		if len(tasks) > 0 {
			fmt.Println("\nActive Tasks:")
			for _, t := range tasks {
				started := t["started"]
				if started != "" {
					started = " (" + started + " ago)"
				}
				fmt.Printf("  %-20s %s%s\n", t["id"], t["expert"], started)
			}
		}
	}
}

func cmdWatch() {
	explicit := ""
	if len(os.Args) > 2 {
		explicit = os.Args[2]
	}

	poolDir, err := config.DiscoverPoolDir(explicit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	sockPath := config.ResolveSockPath(poolDir)
	conn, err := net.DialTimeout("unix", sockPath, 5*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: connecting to daemon (is it running?): %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	// Send subscribe request
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if err := json.NewEncoder(conn).Encode(map[string]string{"method": "subscribe"}); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Read ack
	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "error: reading ack: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "error: no ack from daemon\n")
		}
		os.Exit(1)
	}
	var ack socketResponse
	if err := json.Unmarshal(scanner.Bytes(), &ack); err != nil || ack.Status != "ok" {
		fmt.Fprintf(os.Stderr, "error: subscribe failed: %s\n", ack.Message)
		os.Exit(1)
	}

	// Clear deadline for streaming
	conn.SetDeadline(time.Time{})

	// Handle Ctrl-C cleanly — set flag so scanner error is suppressed
	var stopping atomic.Bool
	watchSigCh := make(chan os.Signal, 1)
	signal.Notify(watchSigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-watchSigCh
		stopping.Store(true)
		conn.Close()
	}()

	fmt.Println("Watching daemon events (Ctrl-C to stop)...")
	fmt.Println()

	// ANSI colors
	const (
		reset  = "\033[0m"
		green  = "\033[32m"
		red    = "\033[31m"
		yellow = "\033[33m"
		cyan   = "\033[36m"
	)

	type event struct {
		Type      string          `json:"type"`
		Timestamp time.Time       `json:"timestamp"`
		Data      json.RawMessage `json:"data"`
	}

	for scanner.Scan() {
		var e event
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}

		ts := e.Timestamp.Format("15:04:05")
		var color, detail string

		switch e.Type {
		case "task.routed":
			color = cyan
			var d struct {
				ID   string `json:"id"`
				From string `json:"from"`
				To   string `json:"to"`
			}
			json.Unmarshal(e.Data, &d)
			detail = fmt.Sprintf("%s -> %s  (%s)", d.From, d.To, d.ID)

		case "expert.spawning":
			color = yellow
			var d struct {
				Expert string `json:"expert"`
				TaskID string `json:"task_id"`
				Model  string `json:"model"`
			}
			json.Unmarshal(e.Data, &d)
			detail = fmt.Sprintf("%s  task=%s  model=%s", d.Expert, d.TaskID, d.Model)

		case "expert.completed":
			color = green
			var d struct {
				Expert   string `json:"expert"`
				TaskID   string `json:"task_id"`
				Duration string `json:"duration"`
				Summary  string `json:"summary"`
			}
			json.Unmarshal(e.Data, &d)
			detail = fmt.Sprintf("%s  task=%s  %s", d.Expert, d.TaskID, d.Duration)
			if d.Summary != "" {
				if len(d.Summary) > 60 {
					d.Summary = d.Summary[:60] + "..."
				}
				detail += "  " + d.Summary
			}

		case "expert.failed":
			color = red
			var d struct {
				Expert   string `json:"expert"`
				TaskID   string `json:"task_id"`
				ExitCode int    `json:"exit_code"`
			}
			json.Unmarshal(e.Data, &d)
			detail = fmt.Sprintf("%s  task=%s  exit=%d", d.Expert, d.TaskID, d.ExitCode)

		case "task.cancelled":
			color = red
			var d struct {
				TaskID string `json:"task_id"`
			}
			json.Unmarshal(e.Data, &d)
			detail = d.TaskID

		case "task.unblocked":
			color = green
			var d struct {
				TaskID string `json:"task_id"`
				Expert string `json:"expert"`
			}
			json.Unmarshal(e.Data, &d)
			detail = fmt.Sprintf("%s -> %s", d.TaskID, d.Expert)

		default:
			detail = string(e.Data)
		}

		fmt.Printf("[%s] %s%-18s%s %s\n", ts, color, e.Type, reset, detail)
	}

	if err := scanner.Err(); err != nil && !stopping.Load() {
		fmt.Fprintf(os.Stderr, "error: stream interrupted: %v\n", err)
		os.Exit(1)
	}
}

// socketResponse mirrors the daemon's response type for CLI deserialization.
type socketResponse struct {
	Status  string          `json:"status"`
	Data    json.RawMessage `json:"data,omitempty"`
	Message string          `json:"message,omitempty"`
}

// connectAndSend dials the daemon socket, sends a method request, and reads the response.
func connectAndSend(sockPath, method string) (*socketResponse, error) {
	conn, err := net.DialTimeout("unix", sockPath, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connecting to daemon (is it running?): %w", err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	req := map[string]string{"method": method}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("reading response: %w", err)
		}
		return nil, fmt.Errorf("no response from daemon")
	}

	var resp socketResponse
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return &resp, nil
}

// cmdMCP starts the stdio MCP server. Stdout is the MCP transport; logs go to stderr.
//
// Two invocation modes:
//
//	agent-pool mcp --pool <dir> --expert <name>         Expert MCP server
//	agent-pool mcp --pool <dir> --role <architect>      Built-in role MCP server
func cmdMCP() {
	flags := parseFlags(2, "pool", "expert", "role", "shared")

	poolDir := flags["pool"]
	expertName := flags["expert"]
	role := flags["role"]
	isShared := flags["shared"] == "true"

	// --role and --expert are mutually exclusive
	if role != "" && expertName != "" {
		fmt.Fprintf(os.Stderr, "error: --role and --expert are mutually exclusive\n")
		os.Exit(1)
	}
	if role != "" {
		expertName = role
	}

	if poolDir == "" || expertName == "" {
		fmt.Fprintf(os.Stderr, "usage: agent-pool mcp --pool <dir> --expert <name>\n")
		fmt.Fprintf(os.Stderr, "       agent-pool mcp --pool <dir> --role <architect>\n")
		os.Exit(1)
	}

	cfg := &agentmcp.ServerConfig{
		PoolDir:    poolDir,
		ExpertName: expertName,
		Role:       role,
		IsShared:   isShared,
		Logger:     newStderrLogger(),
	}

	if isShared {
		cfg.SharedOverlayDir = filepath.Join(poolDir, "shared-state", expertName)
	}

	// Load pool config to get approval mode for architect.
	// Fail-closed: if config can't be loaded, the architect must not start
	// with an empty approval mode (which would bypass human approval).
	if role == "architect" {
		poolCfg, err := config.LoadPool(poolDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error loading pool config for architect: %v\n", err)
			os.Exit(1)
		}
		cfg.ApprovalMode = poolCfg.Architect.ApprovalMode
	}

	if err := agentmcp.Run(context.Background(), cfg); err != nil {
		fmt.Fprintf(os.Stderr, "mcp server error: %v\n", err)
		os.Exit(1)
	}
}

func cmdFlush() {
	flags := parseFlags(2, "pool", "expert", "task", "shared")

	poolDir := flags["pool"]
	expertName := flags["expert"]
	taskID := flags["task"]
	isShared := flags["shared"] == "true"

	if poolDir == "" || expertName == "" {
		fmt.Fprintf(os.Stderr, "usage: agent-pool flush --pool <dir> --expert <name> --task <id>\n")
		os.Exit(1)
	}

	cfg := &hooks.FlushConfig{
		PoolDir:    poolDir,
		ExpertName: expertName,
		TaskID:     taskID,
		IsShared:   isShared,
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

func cmdSeed() {
	flags := parseFlags(2, "pool", "expert")

	poolDir := flags["pool"]
	expertName := flags["expert"]

	if expertName == "" {
		fmt.Fprintf(os.Stderr, "usage: agent-pool seed --pool <dir> --expert <name>\n")
		os.Exit(1)
	}

	var err error
	if poolDir == "" {
		poolDir, err = config.DiscoverPoolDir("")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}

	cfg, err := config.LoadPool(poolDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading pool config: %v\n", err)
		os.Exit(1)
	}

	// Validate expert exists in pool config or shared includes
	found := false
	if _, ok := cfg.Experts[expertName]; ok {
		found = true
	}
	for _, name := range cfg.Shared.Include {
		if name == expertName {
			found = true
			break
		}
	}
	if !found {
		fmt.Fprintf(os.Stderr, "error: expert %q not found in pool config (check [experts] or [shared] sections)\n", expertName)
		os.Exit(1)
	}

	// Read identity if available (gives the researcher context)
	var identityContext string
	expertDir := mail.ResolveExpertDir(poolDir, expertName)
	if data, readErr := os.ReadFile(filepath.Join(expertDir, "identity.md")); readErr == nil {
		identityContext = string(data)
	}

	// Compose seed task
	var body strings.Builder
	body.WriteString("## Cold-Start Seed Task\n\n")
	body.WriteString(fmt.Sprintf("**Target expert:** %s\n\n", expertName))

	if cfg.Pool.ProjectDir != "" {
		body.WriteString(fmt.Sprintf("**Project directory:** %s\n\n", cfg.Pool.ProjectDir))
	}

	if identityContext != "" {
		body.WriteString("### Expert Identity\n\n")
		body.WriteString(identityContext)
		body.WriteString("\n\n")
	}

	body.WriteString("### Instructions\n\n")
	body.WriteString("Explore the project codebase and create initial state.md for this expert.\n")
	body.WriteString("Focus on:\n")
	body.WriteString("- Key files and patterns relevant to this expert's domain\n")
	body.WriteString("- Important APIs, endpoints, or interfaces\n")
	body.WriteString("- Current status of the domain (what works, what's in progress)\n")
	body.WriteString("- Any conventions or gotchas specific to this area\n\n")
	body.WriteString("Use `write_expert_state` to save the initial state.\n")

	msg := &mail.Message{
		ID:        fmt.Sprintf("seed-%s-%d", expertName, time.Now().UnixMilli()),
		From:      "cli",
		To:        "researcher",
		Type:      mail.TypeTask,
		Priority:  mail.PriorityNormal,
		Timestamp: time.Now().UTC(),
		Body:      body.String(),
	}

	if err := mail.Post(poolDir, msg); err != nil {
		fmt.Fprintf(os.Stderr, "error posting seed task: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Seed task posted for expert %q (id: %s)\n", expertName, msg.ID)
}

func printUsage() {
	fmt.Println(`agent-pool — process supervisor for Claude Code expert sessions

Usage:
  agent-pool start [pool-dir]                          Start the daemon for a pool
  agent-pool stop [pool-dir]                           Stop a running daemon
  agent-pool status [pool-dir]                         Show daemon status
  agent-pool watch [pool-dir]                          Stream daemon events
  agent-pool mcp --pool <dir> --expert <name>          Start expert MCP server (stdio)
  agent-pool mcp --pool <dir> --role <role>            Start built-in role MCP server
  agent-pool seed --pool <dir> --expert <name>                Cold-start expert state via researcher
  agent-pool flush --pool <dir> --expert <name> --task <id>   Stop hook: verify state
  agent-pool guard --pool <dir> --expert <name> --path <file> PreToolUse hook: ownership guard
  agent-pool version                                   Print version
  agent-pool help                                      Show this help

Roles:
  architect    Contract definition, task delegation, verification
  concierge    User-facing coordination (read/write path tools)
  researcher   Enrichment and curation

Examples:
  agent-pool start ~/.agent-pool/pools/api-gateway
  agent-pool stop
  agent-pool status
  agent-pool mcp --pool ./my-pool --role concierge`)
}
