# ADR-0001: Ephemeral Sessions Over MCP Server Mode

- **Status:** Accepted
- **Date:** 2026-04-03
- **Context:** Choosing the execution model for expert sessions

## Decision

Expert sessions use ephemeral `claude -p` (headless print mode) invocations, not Claude Code's MCP server mode.

MCP is used as the **communication layer** between roles and the daemon (typed pool tools), not as the execution primitive for experts.

## Context

Claude Code can run as an MCP server via `startMCPServer()`, exposing `tools/list` and `tools/call` endpoints over stdio. This allows external MCP clients to invoke Claude Code's built-in tools (Read, Write, Bash, Grep, etc.) individually.

We considered two execution models for expert sessions:

### Option A: MCP Server Mode (rejected)

Run each expert as a persistent Claude Code MCP server process. The daemon sends individual tool calls via MCP protocol.

### Option B: Ephemeral `claude -p` (accepted)

Spawn a fresh `claude -p` session per task. The expert reasons autonomously, decides which tools to use, and dies when done. State externalized to disk.

## Rationale

### MCP server mode exposes tools, not reasoning

The MCP server provides `tools/call` — execute a specific tool with specific arguments. It does NOT provide "here's a task, figure out how to do it." The agentic behavior — reasoning about a task, selecting tools, iterating on errors, deciding when to stop — only happens in the `claude -p` path where the model controls the query loop.

Using MCP server mode would require the Go daemon to reimplement Claude Code's query engine: send prompt to API, parse tool calls from the response, execute them via MCP, send results back, repeat until done. This is hundreds of lines of orchestration logic that `claude -p` already handles.

### Persistent sessions reintroduce the core problem

Agent Pool exists because monolithic sessions accumulate context that can't be selectively pruned. Running experts as persistent MCP servers would mean:

- Context accumulates across tasks (the problem we're solving)
- Memory cost at rest (violates "zero tokens at rest")
- State lives in the opaque conversation history (not inspectable, not git-trackable)
- Recovery from corruption requires killing the session (losing all accumulated context)
- No selective forgetting (the `--resume` trap)

Ephemeral sessions with externalized state solve all of these:

- Fresh context per task (~5-15% budget instead of 80-90%)
- Zero cost when idle
- State on disk (inspectable, diffable, promotable)
- Recovery = respawn (state survived on disk)
- Knowledge promotion ladder controls what persists

### MCP has the right role already

The architecture uses MCP in the right place — as the communication protocol between the daemon and active sessions:

```
Daemon spawns:  claude -p  ←─MCP─→  agent-pool mcp --expert auth
                (expert)            (daemon's MCP server providing pool tools)
```

The expert session uses MCP tools (`pool_update_state`, `pool_send_response`, `pool_recall`) to interact with the pool in a typed, validated way. The daemon's MCP server is the integration keystone. But the expert itself runs as a full agentic session, not as an MCP server.

## Consequences

- Expert sessions are fire-and-forget subprocesses (`os/exec`)
- The daemon captures output via `--output-format stream-json` piped from stdout
- No persistent connections between daemon and experts (process lifecycle = session lifecycle)
- The daemon must assemble the full prompt (identity + state + errors + task) before each spawn
- Session-to-session continuity relies entirely on externalized state files, not conversation history
