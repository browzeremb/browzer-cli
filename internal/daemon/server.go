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
	// WorkflowKeepalive is how long a per-path workflow drainer stays
	// alive after the queue goes empty. Zero falls back to
	// defaultQueueIdleTimeout (30 min). Sourced from the
	// `daemon.workflow_keepalive_seconds` config key by daemon_cmd.
	WorkflowKeepalive time.Duration
}

// Server is the Browzer daemon JSON-RPC server.
type Server struct {
	opts      Options
	startedAt time.Time
	mu        sync.Mutex // guards listener
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
	// workflowDispatcher owns the per-path FIFO + drainer goroutines that
	// serialise workflow.json mutations. nil-safe: when nil (e.g. tests
	// that construct a bare Server), WorkflowMutate returns
	// "workflow_dispatcher_disabled". Allocated by NewServer.
	workflowDispatcher *workflowDispatcher
	// capabilities is the static list of feature strings the daemon
	// advertises via Health. Frozen at startup. Order matters for stable
	// diffs in tests/snapshots.
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
	s.workflowDispatcher = newWorkflowDispatcher(opts.WorkflowKeepalive)
	// Baseline capabilities reflecting the methods this binary always
	// supports. WorkflowMutate is added when the dispatcher exists (always
	// today; left guarded so a future codepath that disables it stays
	// honest).
	s.capabilities = []string{
		"read.v1",
		"track.v1",
		"session-register.v1",
	}
	if s.workflowDispatcher != nil {
		s.capabilities = append(s.capabilities, "workflow.v1", "workflow.fsync.v1")
		// Register WorkflowMutate eagerly. Unlike Read/Track/SessionRegister
		// (wired by methods.Wire with external Deps), WorkflowMutate has no
		// external dependencies — the dispatcher + workflow package are the
		// whole story. This lets tests that don't call Wire still exercise
		// the verb.
		s.handlers["WorkflowMutate"] = s.handleWorkflowMutate
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
func (s *Server) lookupHandler(method string) (Handler, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
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

// codedError is the handler-side error carrier that lets a method return a
// JSON-RPC error with a non-default code. The default `-32000` (server error)
// is fine for transient/internal failures; method-specific contracts (e.g.
// `Daemon.Version`/`WorkflowMutate` returning `-32602` "invalid params" on
// protocol-version mismatch) need control over the code so callers can
// branch on it without string-matching.
type codedError struct {
	code    int
	message string
}

func (c *codedError) Error() string { return c.message }

// newCodedError returns an error carrying a specific JSON-RPC error code.
// The connection-level dispatch loop unwraps it and emits the matching
// `{code, message}` pair to the wire.
func newCodedError(code int, msg string) error { return &codedError{code: code, message: msg} }

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
		h, ok := s.lookupHandler(req.Method)
		if !ok {
			s.writeErr(conn, req.ID, -32601, "method_not_found: "+req.Method)
			s.queueLen.Add(-1)
			continue
		}
		result, err := h(ctx, req.Params)
		if err != nil {
			code := -32000
			var ce *codedError
			if errors.As(err, &ce) {
				code = ce.code
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
	return map[string]any{
		"uptimeSec":    int(time.Since(s.startedAt).Seconds()),
		"queueLen":     s.queueLen.Load(),
		"dbPath":       s.opts.DBPath,
		"capabilities": s.capabilities,
	}, nil
}

// RegisterHandler is the post-Wire override hook used by tests. Re-export
// the capability list adjustment when a handler is added at runtime: tests
// that inject a stub WorkflowMutate handler still want the matching
// capability advertised. We DO NOT auto-add — registry semantics are
// "register OR override", not "register AND advertise". Capabilities are
// authored by NewServer; tests wanting custom advertise-sets call
// SetCapabilities.

// SetCapabilities replaces the advertised capability list. Tests that
// stand up a degraded daemon (e.g. "no workflow.v1 to verify fallback")
// call SetCapabilities([]string{...}) before Serve.
func (s *Server) SetCapabilities(c []string) {
	cp := make([]string, len(c))
	copy(cp, c)
	s.capabilities = cp
}

// Capabilities returns a copy of the advertised capability list. Mostly
// useful for tests; production callers read the list via Health over the
// socket.
func (s *Server) Capabilities() []string {
	cp := make([]string, len(s.capabilities))
	copy(cp, s.capabilities)
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
