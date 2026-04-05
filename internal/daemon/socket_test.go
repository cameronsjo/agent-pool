// Test plan for socket.go:
//
// Socket lifecycle:
//   - TestSocket_Stop: connect, send stop, verify ok response, daemon exits
//   - TestSocket_Unknown: send unknown, get error, daemon stays up
//   - TestSocket_StaleCleanup: stale socket file doesn't block start
//   - TestSocket_RemovedOnShutdown: file cleaned up after exit
//   - TestSocket_Status: verify status response has pool name and experts
package daemon_test

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// shortTempDir creates a temp directory with a short path, avoiding macOS Unix
// socket path length limits (104 bytes). t.TempDir() generates paths too long
// for socket files when test names are verbose.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ap-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// connectSocket dials the daemon's unix socket with a short timeout.
// poolDir should be a short path created by shortTempDir when daemon.sock
// is at {poolDir}/daemon.sock.
func connectSocket(t *testing.T, poolDir string) net.Conn {
	t.Helper()
	sockPath := filepath.Join(poolDir, "daemon.sock")

	// The socket may not be ready immediately after daemon start.
	var conn net.Conn
	var err error
	for i := 0; i < 20; i++ {
		conn, err = net.DialTimeout("unix", sockPath, 500*time.Millisecond)
		if err == nil {
			return conn
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("failed to connect to socket after retries: %v", err)
	return nil
}

// sendSocketRequest writes a JSON request and reads the JSON response.
func sendSocketRequest(t *testing.T, conn net.Conn, method string) map[string]any {
	t.Helper()

	conn.SetDeadline(time.Now().Add(5 * time.Second))

	req := map[string]string{"method": method}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatalf("sending request: %v", err)
	}

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		t.Fatalf("no response: %v", scanner.Err())
	}

	var resp map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		t.Fatalf("parsing response: %v", err)
	}
	return resp
}

func TestSocket_Stop(t *testing.T) {
	poolDir := shortTempDir(t)

	cfg := writePoolConfig(t, poolDir, `[pool]
name = "socket-test"
project_dir = "PROJECT_DIR"

[experts.auth]
`)

	_, errCh := startTestDaemon(t, cfg, poolDir, &fakeSpawner{})

	conn := connectSocket(t, poolDir)
	resp := sendSocketRequest(t, conn, "stop")
	conn.Close()

	if resp["status"] != "ok" {
		t.Errorf("stop status = %v, want ok", resp["status"])
	}

	// Daemon should exit after stop
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("daemon returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("daemon did not shut down after stop")
	}
}

func TestSocket_Unknown(t *testing.T) {
	poolDir := shortTempDir(t)

	cfg := writePoolConfig(t, poolDir, `[pool]
name = "socket-test"
project_dir = "PROJECT_DIR"

[experts.auth]
`)

	cancel, errCh := startTestDaemon(t, cfg, poolDir, &fakeSpawner{})

	conn := connectSocket(t, poolDir)
	resp := sendSocketRequest(t, conn, "bogus")
	conn.Close()

	if resp["status"] != "error" {
		t.Errorf("status = %v, want error", resp["status"])
	}
	msg, _ := resp["message"].(string)
	if msg != "unknown method: bogus" {
		t.Errorf("message = %q, want 'unknown method: bogus'", msg)
	}

	// Daemon should still be running
	shutdownDaemon(t, cancel, errCh)
}

func TestSocket_StaleCleanup(t *testing.T) {
	poolDir := shortTempDir(t)

	// Create a stale socket file before starting the daemon
	sockPath := filepath.Join(poolDir, "daemon.sock")
	if err := os.WriteFile(sockPath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := writePoolConfig(t, poolDir, `[pool]
name = "socket-test"
project_dir = "PROJECT_DIR"

[experts.auth]
`)

	cancel, errCh := startTestDaemon(t, cfg, poolDir, &fakeSpawner{})

	// Daemon should start despite stale socket — verify by connecting
	conn := connectSocket(t, poolDir)
	resp := sendSocketRequest(t, conn, "status")
	conn.Close()

	if resp["status"] != "ok" {
		t.Errorf("status = %v, want ok", resp["status"])
	}

	shutdownDaemon(t, cancel, errCh)
}

func TestSocket_RemovedOnShutdown(t *testing.T) {
	poolDir := shortTempDir(t)

	cfg := writePoolConfig(t, poolDir, `[pool]
name = "socket-test"
project_dir = "PROJECT_DIR"

[experts.auth]
`)

	cancel, errCh := startTestDaemon(t, cfg, poolDir, &fakeSpawner{})

	// Verify socket exists while running
	sockPath := filepath.Join(poolDir, "daemon.sock")
	if _, err := os.Stat(sockPath); os.IsNotExist(err) {
		t.Fatal("socket file should exist while daemon is running")
	}

	shutdownDaemon(t, cancel, errCh)

	// Socket file should be removed after shutdown
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Error("socket file should be removed after shutdown")
	}
}

func TestSocket_Status(t *testing.T) {
	poolDir := shortTempDir(t)

	cfg := writePoolConfig(t, poolDir, `[pool]
name = "status-test"
project_dir = "PROJECT_DIR"

[experts.auth]
[experts.frontend]
`)

	cancel, errCh := startTestDaemon(t, cfg, poolDir, &fakeSpawner{})

	conn := connectSocket(t, poolDir)
	resp := sendSocketRequest(t, conn, "status")
	conn.Close()

	if resp["status"] != "ok" {
		t.Fatalf("status = %v, want ok", resp["status"])
	}

	data, ok := resp["data"].(map[string]any)
	if !ok {
		t.Fatalf("data is not a map: %T", resp["data"])
	}

	if data["pool"] != "status-test" {
		t.Errorf("pool = %v, want status-test", data["pool"])
	}

	if data["state"] != "running" {
		t.Errorf("state = %v, want running", data["state"])
	}

	experts, ok := data["experts"].([]any)
	if !ok {
		t.Fatalf("experts is not a list: %T", data["experts"])
	}
	if len(experts) != 2 {
		t.Errorf("experts count = %d, want 2", len(experts))
	}

	shutdownDaemon(t, cancel, errCh)
}
