# Running agent-pool with launchd

launchd is macOS's service manager. Use it to run the daemon in the background
so it starts automatically on login.

## Install

1. Copy the plist template:

```bash
cp scripts/com.agent-pool.daemon.plist ~/Library/LaunchAgents/
```

2. Edit the plist and replace placeholders:

```bash
# Replace AGENT_POOL_BINARY with the full path to the binary
sed -i '' "s|AGENT_POOL_BINARY|$(which agent-pool)|g" \
  ~/Library/LaunchAgents/com.agent-pool.daemon.plist

# Replace POOL_DIR with your pool directory
sed -i '' "s|POOL_DIR|$HOME/.agent-pool/pools/my-pool|g" \
  ~/Library/LaunchAgents/com.agent-pool.daemon.plist
```

3. Load the service:

```bash
launchctl load ~/Library/LaunchAgents/com.agent-pool.daemon.plist
```

## Stop

```bash
# Graceful stop via socket (preferred)
agent-pool stop

# Or via launchctl (sends SIGTERM, daemon drains gracefully)
launchctl stop com.agent-pool.daemon
```

## Unload

Remove the service entirely:

```bash
launchctl unload ~/Library/LaunchAgents/com.agent-pool.daemon.plist
rm ~/Library/LaunchAgents/com.agent-pool.daemon.plist
```

## Logs

The daemon writes to `{poolDir}/daemon.log` as usual. launchd captures
any output that bypasses slog to `launchd-stdout.log` and `launchd-stderr.log`
in the pool directory.

## Notes

- `KeepAlive` is false — the daemon runs once and exits on stop. Use
  `agent-pool start` to restart manually, or set `KeepAlive` to true
  if you want launchd to auto-restart on crash.
- `RunAtLoad` is true — the daemon starts when you log in.
- Double-signal works: `launchctl stop` sends SIGTERM (graceful drain),
  a second stop during drain forces immediate exit.
