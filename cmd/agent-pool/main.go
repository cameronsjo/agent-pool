package main

import (
	"fmt"
	"os"

	"git.sjo.lol/cameron/agent-pool/internal/config"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "start":
		cmdStart()
	case "version":
		fmt.Println("agent-pool v0.1.0-dev")
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

	fmt.Printf("agent-pool starting for pool: %s\n", cfg.Pool.Name)
	fmt.Printf("  project dir: %s\n", cfg.Pool.ProjectDir)
	fmt.Printf("  experts: %d\n", len(cfg.Experts))

	// TODO(#1): implement daemon.Run(cfg)
}

func printUsage() {
	fmt.Println(`agent-pool — process supervisor for Claude Code expert sessions

Usage:
  agent-pool start [pool-dir]   Start the daemon for a pool
  agent-pool version            Print version
  agent-pool help               Show this help

Examples:
  agent-pool start ~/.agent-pool/pools/api-gateway
  agent-pool start              # uses current directory`)
}
