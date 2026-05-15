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
//
// mu is a sync.RWMutex so the per-request handler lookup (read-mostly: the
// registry is mutated only at startup or by tests) can use RLock and avoid
// serializing concurrent requests behind one another.
type Server struct {
	opts      Options
	startedAt time.Time
	mu        sync.RWMutex // guards listener + handlers
	listener  net.Listener
	handlers  map[string]Handler
	queueLen  atomic.Int64
	lastReqAt atomic.Int64 // unix nano
	// tokensEconomized is a cumulative-since-daemon-start counter of
	// `savedTokens` recorded via `Track` / `RecordSavedTokens`. Reset only on
	// daemon restart (acceptable per the dashboard KPI PRD — server-side
	// aggregation in apps/api is the canonical cumulative value; this counter
	// is the daemon's view exposed via TokensEconomized RPC + GET /metrics/
	// tokens-economized for diagnostics).
	tokensEconomized atomic.Int64
	stopOnce         sync.Once
	stopped          chan struct{}
	// capabilities is the list of feature strings the daemon advertises via
	// Health. Initialized in NewServer; may be replaced via SetCapabilities
	// (tests only). All reads and writes are guarded by s.mu. Order matters
	// for stable diffs in tests/snapshots.
	capabilities []string
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
		"Health":           s.handleHealth,
		"Shutdown":         s.handleShutdown,
		"TokensEconomized": s.handleTokensEconomized,
		"Daemon.Version":   s.handleDaemonVersion,
		// Read, Track, SessionRegister wired by methods.go (Task 3).
	}
	// Baseline capabilities reflecting the methods this binary always
	// supports.
	s.capabilities = []string{
		"read.v1",
		"track.v1",
		"session-register.v1",
	}
	return s
}

// RecordSavedTokens atomically adds `delta` to the cumulative
// tokens-economized counter. Negative or zero deltas are ignored.
//
// Called from the Track RPC dependency (daemon_cmd.go) on every Track event,
// so the counter shadows the SQLite tracker without taking ownership of it
// (subsystem isolation: a broken tracker MUST NOT lose this number, but the
// authoritative cumulative is always the server-side SUM(saved_tokens)).
func (s *Server) RecordSavedTokens(delta int) {
	if delta <= 0 {
		return
	}
	s.tokensEconomized.Add(int64(delta))
}

// TokensEconomized returns the daemon's in-memory cumulative counter.
// Exported so the parent process / tests can read it directly without
// going over the JSON-RPC socket.
func (s *Server) TokensEconomized() int64 {
	return s.tokensEconomized.Load()
}

// RegisterHandler attaches a handler to a method name. Used by methods.go
// to wire Read/Track/SessionRegister without circular references.
//
// Concurrency (F-01, 2026-05-04): the write is guarded by s.mu so a
// late RegisterHandler call (e.g. an integration test that wires a stub
// handler after Serve has started) cannot race with the concurrent read
// in handleConn. The matching read in handleConn also takes s.mu via
// lookupHandler.
func (s *Server) RegisterHandler(method string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = h
}

// lookupHandler returns the registered handler for method, holding s.mu
// for the duration of the read. Mirrors RegisterHandler's lock so the
// map read in handleConn never races with a concurrent write (F-01).
// Uses RLock so concurrent requests don't serialize against each other —
// the handlers map is read-mostly (registered once at startup).
func (s *Server) lookupHandler(method string) (Handler, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h, ok := s.handlers[method]
	return h, ok
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
		s.mu.RLock()
		l := s.listener
		s.mu.RUnlock()
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

// handleConn reads newline-delimited JSON-RPC 2.0 requests from conn and
// writes responses until the connection is closed.
//
// Error code selection: transport/protocol errors use fixed JSON-RPC codes
// (-32700 parse_error, -32601 method_not_found). Handler errors default to
// -32000 (server_error). A handler may opt into a more specific code by
// returning an error that satisfies interface{ Code() int }; handleConn
// type-asserts the error and uses Code() when present. This lets future
// handlers signal -32602 (invalid_params) or any other JSON-RPC code without
// requiring changes here.
func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	rdr := bufio.NewReader(conn)
	for {
		// ReadBytes returns a freshly-allocated slice so json.Unmarshal can
		// consume the line without the [string → []byte] copy ReadString's
		// return path forced every request. json.Unmarshal does not retain
		// the input bytes past its return, so the per-iteration alloc is
		// the only ownership concern, and it is bounded by the line size.
		line, err := rdr.ReadBytes('\n')
		if err != nil {
			// io.EOF on clean client close is expected; other errors logged caller-side at -vv.
			return
		}
		s.lastReqAt.Store(time.Now().UnixNano())
		s.queueLen.Add(1)
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			s.writeErr(conn, nil, -32700, "parse_error")
			s.queueLen.Add(-1)
			continue
		}
		h, ok := s.lookupHandler(req.Method)
		if !ok {
			s.writeErr(conn, req.ID, -32601, "method_not_found: "+req.Method)
			s.queueLen.Add(-1)
			continue
		}
		result, err := h(ctx, req.Params)
		if err != nil {
			code := -32000
			if cer, ok := err.(interface{ Code() int }); ok {
				code = cer.Code()
			}
			s.writeErr(conn, req.ID, code, err.Error())
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
	// Use a flat map (not the typed HealthResponse struct) so that
	// hand-tested daemon clients keep working — the historic shape is
	// preserved verbatim and we only ADD `capabilities` to it. Older
	// clients ignore unknown fields; newer clients (HasCapability) read
	// the new field.
	//
	// Take a snapshot of capabilities under RLock so a concurrent
	// SetCapabilities call cannot mutate the slice while we hold a
	// reference to it.
	s.mu.RLock()
	caps := make([]string, len(s.capabilities))
	copy(caps, s.capabilities)
	s.mu.RUnlock()

	return map[string]any{
		"uptimeSec":    int(time.Since(s.startedAt).Seconds()),
		"queueLen":     s.queueLen.Load(),
		"dbPath":       s.opts.DBPath,
		"capabilities": caps,
	}, nil
}

// SetCapabilities replaces the advertised capability list. Tests that
// stand up a degraded daemon (e.g. one that drops a normally-advertised
// capability to verify fallback) call SetCapabilities([]string{...})
// before or after Serve. Registry semantics are "register OR override",
// not "register AND advertise" — adding a handler via RegisterHandler does
// NOT auto-add a matching capability; callers must opt in via this
// method when they want the advertise side to track.
//
// Guarded by s.mu so concurrent Health reads never race with a
// mid-flight SetCapabilities call.
func (s *Server) SetCapabilities(c []string) {
	cp := make([]string, len(c))
	copy(cp, c)
	s.mu.Lock()
	s.capabilities = cp
	s.mu.Unlock()
}

// Capabilities returns a copy of the advertised capability list. Mostly
// useful for tests; production callers read the list via Health over the
// socket.
//
// Guarded by s.mu so concurrent SetCapabilities calls never race with
// this read.
func (s *Server) Capabilities() []string {
	s.mu.RLock()
	cp := make([]string, len(s.capabilities))
	copy(cp, s.capabilities)
	s.mu.RUnlock()
	return cp
}

func (s *Server) handleShutdown(_ context.Context, _ json.RawMessage) (any, error) {
	go func() {
		time.Sleep(50 * time.Millisecond) // allow response to flush
		s.Stop()
	}()
	return map[string]any{"ok": true}, nil
}

// handleTokensEconomized returns the cumulative-since-daemon-start counter
// of saved tokens recorded by Track. The result includes the daemon start
// time so callers can disambiguate between "low number = quiet daemon" and
// "low number = recently restarted".
func (s *Server) handleTokensEconomized(_ context.Context, _ json.RawMessage) (any, error) {
	return map[string]any{
		"tokensEconomized": s.tokensEconomized.Load(),
		"since":            s.startedAt.UTC().Format(time.RFC3339),
	}, nil
}
