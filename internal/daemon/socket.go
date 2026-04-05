package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"
)

// socketRequest is the NDJSON request format for CLI→daemon communication.
type socketRequest struct {
	Method string `json:"method"`
}

// socketResponse is the NDJSON response format for daemon→CLI communication.
type socketResponse struct {
	Status  string `json:"status"`            // "ok" or "error"
	Data    any    `json:"data,omitempty"`     // method-specific payload
	Message string `json:"message,omitempty"` // error description
}

// socketServer listens on a Unix domain socket for CLI commands.
type socketServer struct {
	listener net.Listener
	logger   *slog.Logger
	daemon   *Daemon
	cancel   context.CancelFunc // cancels the daemon's child context (for stop)
	sockPath string
}

// newSocketServer creates a socket server at the given path. Removes any stale
// socket file from a previous crash before listening.
func newSocketServer(sockPath string, d *Daemon, cancel context.CancelFunc) (*socketServer, error) {
	// Remove stale socket — if the daemon crashed, the file lingers.
	if err := os.Remove(sockPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("removing stale socket: %w", err)
	}

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", sockPath, err)
	}

	return &socketServer{
		listener: listener,
		logger:   d.logger,
		daemon:   d,
		cancel:   cancel,
		sockPath: sockPath,
	}, nil
}

// serve accepts connections until ctx is cancelled. Each connection is handled
// in its own goroutine.
func (s *socketServer) serve(ctx context.Context) {
	go func() {
		<-ctx.Done()
		s.listener.Close()
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// Expected when listener is closed during shutdown.
			if ctx.Err() != nil {
				return
			}
			s.logger.Warn("Socket accept error", "error", err)
			continue
		}
		go s.handleConn(ctx, conn)
	}
}

// handleConn reads a single NDJSON request, dispatches by method, writes the
// response, and closes the connection.
func (s *socketServer) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	scanner := bufio.NewScanner(conn)
	if !scanner.Scan() {
		return
	}

	var req socketRequest
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		s.writeResponse(conn, socketResponse{
			Status:  "error",
			Message: "invalid request: " + err.Error(),
		})
		return
	}

	switch req.Method {
	case "stop":
		s.writeResponse(conn, socketResponse{
			Status: "ok",
			Data:   map[string]string{"message": "shutting down"},
		})
		s.cancel()

	case "status":
		data := s.daemon.Status()
		s.writeResponse(conn, socketResponse{
			Status: "ok",
			Data:   data,
		})

	case "subscribe":
		s.handleSubscribe(ctx, conn)

	default:
		s.writeResponse(conn, socketResponse{
			Status:  "error",
			Message: fmt.Sprintf("unknown method: %s", req.Method),
		})
	}
}

// handleSubscribe is a placeholder for Phase 4 event streaming.
func (s *socketServer) handleSubscribe(_ context.Context, conn net.Conn) {
	s.writeResponse(conn, socketResponse{
		Status:  "error",
		Message: "subscribe not implemented",
	})
}

// writeResponse encodes a response as a single JSON line.
func (s *socketServer) writeResponse(conn net.Conn, resp socketResponse) {
	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	json.NewEncoder(conn).Encode(resp)
}

// close shuts down the listener and removes the socket file.
func (s *socketServer) close() error {
	s.listener.Close()
	return os.Remove(s.sockPath)
}
