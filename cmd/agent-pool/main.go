package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
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
	case "init":
		cmdInit()
	case "add":
		cmdAdd()
	case "list":
		cmdList()
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
		fmt.Println("agent-pool v0.9.0")
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func cmdInit() {
	poolDir := ".agent-pool"
	if len(os.Args) > 2 {
		poolDir = os.Args[2]
	}
	poolDir = expandTilde(poolDir)

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	poolName := filepath.Base(cwd)
	if err := initPool(poolDir, poolName, cwd); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Initialized pool %q in %s\n", poolName, poolDir)
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Add experts:")
	fmt.Println("     agent-pool add backend")
	fmt.Println("     agent-pool add frontend --model opus")
	fmt.Println()
	fmt.Println("  2. Start the daemon:")
	fmt.Println("     agent-pool start")
}

func cmdAdd() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: agent-pool add <name> [--model <model>]\n")
		os.Exit(1)
	}

	expertName := os.Args[2]
	flags := parseFlagsFromArgs(os.Args[3:], "model")
	model := flags["model"]
	if model != "" && strings.HasPrefix(model, "-") {
		fmt.Fprintf(os.Stderr, "error: --model requires a value\n")
		os.Exit(1)
	}

	poolDir, err := config.DiscoverPoolDir("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if err := addExpert(poolDir, expertName, model); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if model == "" {
		fmt.Printf("Added expert %q (using default model)\n", expertName)
	} else {
		fmt.Printf("Added expert %q (model: %s)\n", expertName, model)
	}
}

// addExpert appends an expert section to pool.toml and creates its directories.
func addExpert(poolDir, name, model string) error {
	// Validate name: must be alphanumeric, hyphens, or underscores (safe for TOML keys and filenames)
	validName := regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	if !validName.MatchString(name) {
		return fmt.Errorf("invalid expert name %q: must contain only letters, digits, hyphens, or underscores", name)
	}
	if config.BuiltinRoleNames[name] {
		return fmt.Errorf("%q is a built-in role, not an expert name", name)
	}

	// Check not already in config
	cfg, err := config.LoadPool(poolDir)
	if err != nil {
		return fmt.Errorf("loading pool config: %w", err)
	}
	if _, exists := cfg.Experts[name]; exists {
		return fmt.Errorf("expert %q already exists in pool config", name)
	}

	// Append to pool.toml
	tomlPath := filepath.Join(poolDir, "pool.toml")
	f, err := os.OpenFile(tomlPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening %s: %w", tomlPath, err)
	}
	defer f.Close()

	section := fmt.Sprintf("\n[experts.%s]\n", name)
	if model != "" {
		section += fmt.Sprintf("model = %q\n", model)
	}
	if _, err := f.WriteString(section); err != nil {
		return fmt.Errorf("writing to %s: %w", tomlPath, err)
	}

	// Create directories
	expertBase := filepath.Join(poolDir, "experts", name)
	for _, sub := range []string{"inbox", "logs"} {
		if err := os.MkdirAll(filepath.Join(expertBase, sub), 0o755); err != nil {
			return fmt.Errorf("creating expert directory: %w", err)
		}
	}

	return nil
}

func cmdList() {
	poolDir, err := config.DiscoverPoolDir("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	cfg, err := config.LoadPool(poolDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	listExperts(poolDir, cfg)
}

// listExperts prints a formatted table of pool-scoped and shared experts.
func listExperts(poolDir string, cfg *config.PoolConfig) {
	type expertInfo struct {
		Name   string
		Model  string
		Scope  string
		Has    []string // which state files exist
	}

	var experts []expertInfo

	// Pool-scoped experts (sorted for stable output)
	expertNames := make([]string, 0, len(cfg.Experts))
	for name := range cfg.Experts {
		expertNames = append(expertNames, name)
	}
	sort.Strings(expertNames)
	for _, name := range expertNames {
		sec := cfg.Experts[name]
		model := sec.Model
		if model == "" {
			model = cfg.Defaults.Model
		}
		info := expertInfo{Name: name, Model: model, Scope: "pool"}
		expertDir := filepath.Join(poolDir, "experts", name)
		for _, file := range []string{"identity.md", "state.md", "errors.md"} {
			if _, err := os.Stat(filepath.Join(expertDir, file)); err == nil {
				info.Has = append(info.Has, strings.TrimSuffix(file, ".md"))
			}
		}
		experts = append(experts, info)
	}

	// Shared experts
	for _, name := range cfg.Shared.Include {
		model := cfg.Defaults.Model
		info := expertInfo{Name: name, Model: model, Scope: "shared"}
		sharedDir, err := config.SharedExpertDir(name)
		if err == nil {
			for _, file := range []string{"identity.md", "state.md", "errors.md"} {
				if _, err := os.Stat(filepath.Join(sharedDir, file)); err == nil {
					info.Has = append(info.Has, strings.TrimSuffix(file, ".md"))
				}
			}
		}
		experts = append(experts, info)
	}

	if len(experts) == 0 {
		fmt.Println("No experts configured. Add one with:")
		fmt.Println("  agent-pool add <name>")
		return
	}

	fmt.Printf("%-20s %-10s %-8s %s\n", "EXPERT", "MODEL", "SCOPE", "STATE")
	fmt.Printf("%-20s %-10s %-8s %s\n", "------", "-----", "-----", "-----")
	for _, e := range experts {
		state := "-"
		if len(e.Has) > 0 {
			state = strings.Join(e.Has, ", ")
		}
		fmt.Printf("%-20s %-10s %-8s %s\n", e.Name, e.Model, e.Scope, state)
	}
}

// initPool creates the pool directory structure and writes a minimal pool.toml.
func initPool(poolDir, poolName, projectDir string) error {
	tomlPath := filepath.Join(poolDir, "pool.toml")
	if _, err := os.Stat(tomlPath); err == nil {
		return fmt.Errorf("%s already exists", tomlPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking %s: %w", tomlPath, err)
	}

	dirs := []string{
		filepath.Join(poolDir, "postoffice"),
		filepath.Join(poolDir, "contracts"),
		filepath.Join(poolDir, "formulas"),
		filepath.Join(poolDir, "architect", "inbox"),
		filepath.Join(poolDir, "architect", "logs"),
		filepath.Join(poolDir, "researcher", "inbox"),
		filepath.Join(poolDir, "researcher", "logs"),
		filepath.Join(poolDir, "concierge", "inbox"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}

	toml := fmt.Sprintf(`[pool]
name = %q
project_dir = %q

[architect]
model = "opus"

[defaults]
model = "sonnet"
`, poolName, projectDir)

	if err := os.WriteFile(tomlPath, []byte(toml), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", tomlPath, err)
	}

	return nil
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

// expandTilde replaces a leading ~ with the user's home directory.
func expandTilde(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		if path == "~" {
			return home
		}
		return filepath.Join(home, path[2:])
	}
	return path
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
  agent-pool init [pool-dir]              Initialize a new pool (default: .agent-pool/)
  agent-pool add <name> [--model <model>] Add an expert to the pool
  agent-pool list                         Show experts and their state
  agent-pool start [pool-dir]             Start the daemon
  agent-pool stop [pool-dir]              Stop the daemon
  agent-pool status [pool-dir]            Daemon health and task summary
  agent-pool watch [pool-dir]             Stream daemon events live
  agent-pool seed --expert <name>         Cold-start an expert via researcher
  agent-pool version                      Print version
  agent-pool help                         Show this help

Getting started:
  agent-pool init                         Create a pool in the current project
  agent-pool add backend                  Add a backend expert
  agent-pool add frontend --model opus    Add a frontend expert on Opus
  agent-pool start                        Start the daemon

Internal (hooks and plumbing):
  agent-pool mcp --pool <dir> --expert <name>          Expert MCP server (stdio)
  agent-pool mcp --pool <dir> --role <role>            Built-in role MCP server
  agent-pool flush --pool <dir> --expert <name> --task <id>   Stop hook
  agent-pool guard --pool <dir> --expert <name> --path <file> Ownership guard`)
}
