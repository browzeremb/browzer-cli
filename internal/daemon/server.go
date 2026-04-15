// Package daemon implements the Browzer daemon: a long-running process
// that listens on a Unix socket and serves the JSON-RPC contract spec'd
// at packages/cli/internal/daemon/contract.md.
package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Options configures a daemon instance.
type Options struct {
	SocketPath string
	// IdleTimeout is the duration of zero requests after which the daemon
	// auto-shuts-down. Zero disables the timeout (used in tests).
	IdleTimeout time.Duration
	// DBPath is reported by the Health method. Optional in tests.
	DBPath string
}

// Server is the Browzer daemon JSON-RPC server.
type Server struct {
	opts      Options
	startedAt time.Time
	mu        sync.Mutex  // guards listener
	listener  net.Listener
	handlers  map[string]Handler
	queueLen  atomic.Int64
	lastReqAt atomic.Int64 // unix nano
	stopOnce  sync.Once
	stopped   chan struct{}
}

// Handler is the signature for a JSON-RPC method handler. It receives the
// raw `params` JSON and returns a result (any JSON-marshalable value) or
// an error.
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// NewServer constructs a daemon with the default method registry.
func NewServer(opts Options) *Server {
	s := &Server{
		opts:    opts,
		stopped: make(chan struct{}),
	}
	s.handlers = map[string]Handler{
		"Health":   s.handleHealth,
		"Shutdown": s.handleShutdown,
		// Read, Track, SessionRegister wired by methods.go (Task 3).
	}
	return s
}

// RegisterHandler attaches a handler to a method name. Used by methods.go
// to wire Read/Track/SessionRegister without circular references.
func (s *Server) RegisterHandler(method string, h Handler) {
	s.handlers[method] = h
}

// Serve listens on the configured socket until ctx is canceled. Blocks.
func (s *Server) Serve(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(s.opts.SocketPath), 0o700); err != nil {
		return fmt.Errorf("mkdir socket dir: %w", err)
	}
	_ = os.Remove(s.opts.SocketPath) // stale socket
	l, err := net.Listen("unix", s.opts.SocketPath)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.mu.Lock()
	s.listener = l
	s.mu.Unlock()
	if err := os.Chmod(s.opts.SocketPath, 0o600); err != nil {
		_ = l.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}
	s.startedAt = time.Now()
	s.lastReqAt.Store(s.startedAt.UnixNano())

	go s.idleWatcher(ctx)
	go func() { <-ctx.Done(); s.Stop() }()

	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}
		go s.handleConn(ctx, conn)
	}
}

// Stop shuts the daemon down idempotently.
func (s *Server) Stop() {
	s.stopOnce.Do(func() {
		s.mu.Lock()
		l := s.listener
		s.mu.Unlock()
		if l != nil {
			_ = l.Close()
		}
		_ = os.Remove(s.opts.SocketPath)
		close(s.stopped)
	})
}

// Stopped returns a channel closed when Stop has run.
func (s *Server) Stopped() <-chan struct{} { return s.stopped }

func (s *Server) idleWatcher(ctx context.Context) {
	if s.opts.IdleTimeout <= 0 {
		return
	}
	t := time.NewTicker(s.opts.IdleTimeout / 4)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			lastNs := s.lastReqAt.Load()
			if time.Since(time.Unix(0, lastNs)) >= s.opts.IdleTimeout {
				if s.queueLen.Load() > 0 {
					continue // request in-flight — wait
				}
				s.Stop()
				return
			}
		}
	}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	rdr := bufio.NewReader(conn)
	for {
		line, err := rdr.ReadString('\n')
		if err != nil {
			// io.EOF on clean client close is expected; other errors logged caller-side at -vv.
			return
		}
		s.lastReqAt.Store(time.Now().UnixNano())
		s.queueLen.Add(1)
		var req rpcRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			s.writeErr(conn, nil, -32700, "parse_error")
			s.queueLen.Add(-1)
			continue
		}
		h, ok := s.handlers[req.Method]
		if !ok {
			s.writeErr(conn, req.ID, -32601, "method_not_found: "+req.Method)
			s.queueLen.Add(-1)
			continue
		}
		result, err := h(ctx, req.Params)
		if err != nil {
			s.writeErr(conn, req.ID, -32000, err.Error())
			s.queueLen.Add(-1)
			continue
		}
		s.writeOK(conn, req.ID, result)
		s.queueLen.Add(-1)
	}
}

func (s *Server) writeOK(w io.Writer, id json.RawMessage, result any) {
	buf, _ := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
	_, _ = w.Write(append(buf, '\n'))
}

func (s *Server) writeErr(w io.Writer, id json.RawMessage, code int, msg string) {
	buf, _ := json.Marshal(rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
	_, _ = w.Write(append(buf, '\n'))
}

func (s *Server) handleHealth(_ context.Context, _ json.RawMessage) (any, error) {
	return map[string]any{
		"uptimeSec": int(time.Since(s.startedAt).Seconds()),
		"queueLen":  s.queueLen.Load(),
		"dbPath":    s.opts.DBPath,
	}, nil
}

func (s *Server) handleShutdown(_ context.Context, _ json.RawMessage) (any, error) {
	go func() {
		time.Sleep(50 * time.Millisecond) // allow response to flush
		s.Stop()
	}()
	return map[string]any{"ok": true}, nil
}
