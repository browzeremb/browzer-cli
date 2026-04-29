package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// capabilityCacheTTL is how long a cached capability set remains fresh.
// 60s strikes a balance between "respond quickly to a freshly-restarted
// daemon that gained workflow.v1" and "don't refetch on every CLI invocation".
const capabilityCacheTTL = 60 * time.Second

// Client is a thin JSON-RPC client over the daemon's Unix socket.
type Client struct {
	sockPath string
	id       atomic.Int64
	timeout  time.Duration

	// now is injectable for tests that exercise the capability cache TTL.
	// nil means "use time.Now". Tests set this to a controllable clock.
	now func() time.Time

	// capsMu guards capsAt + caps + capsWarnOnce.
	capsMu       sync.Mutex
	caps         map[string]bool
	capsAt       time.Time
	capsWarnOnce sync.Once
}

// NewClient returns a daemon client. Default timeout = 2s.
func NewClient(sockPath string) *Client {
	return &Client{sockPath: sockPath, timeout: 2 * time.Second}
}

// nowFn returns the client's clock (or wall clock by default).
func (c *Client) nowFn() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// HealthResult mirrors the daemon's Health response.
//
// Capabilities is nil for daemons predating the 2026-04-29 capability
// negotiation. Treat nil as "no advertised capabilities → caller falls
// back". Empty (non-nil) slice means "daemon supports the protocol but
// declares zero capabilities" — used by tests.
type HealthResult struct {
	UptimeSec    int      `json:"uptimeSec"`
	QueueLen     int      `json:"queueLen"`
	DBPath       string   `json:"dbPath"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// Health calls the daemon's Health method.
func (c *Client) Health(ctx context.Context) (HealthResult, error) {
	var out HealthResult
	if err := c.call(ctx, "Health", struct{}{}, &out); err != nil {
		return HealthResult{}, err
	}
	return out, nil
}

// HasCapability returns true if the daemon advertises `name` in its Health
// response. Result is cached per Client for 60s. Dial / RPC errors during
// the probe collapse to false; the caller is expected to treat any false
// return as "daemon path unavailable, fall back to standalone".
//
// One-shot warning: when a probe succeeds but `workflow.v1` is missing
// (i.e. the daemon is too old to handle WorkflowMutate), we emit a single
// stderr line. This is the only side effect in the otherwise-pure cache
// lookup. Tests can swap stderr via os.Stderr redirection.
func (c *Client) HasCapability(ctx context.Context, name string) bool {
	c.capsMu.Lock()
	caps := c.caps
	capsAt := c.capsAt
	c.capsMu.Unlock()

	if caps != nil && c.nowFn().Sub(capsAt) < capabilityCacheTTL {
		return caps[name]
	}

	h, err := c.Health(ctx)
	if err != nil {
		// Don't cache: a daemon that isn't running today might be running
		// 5 seconds from now, and we want to recheck on the next call.
		// Don't warn either: the dispatch helper already emits a
		// fallback warning that's more contextual than this one.
		return false
	}
	fresh := make(map[string]bool, len(h.Capabilities))
	for _, cap := range h.Capabilities {
		fresh[cap] = true
	}

	c.capsMu.Lock()
	c.caps = fresh
	c.capsAt = c.nowFn()
	// Only warn when the daemon answered Health BUT lacks workflow.v1 —
	// this is the "you're running an old daemon, restart it" path. If the
	// daemon was simply unreachable, the caller's fallback warning is
	// already the right user-facing signal.
	if !fresh["workflow.v1"] && len(fresh) > 0 {
		c.capsWarnOnce.Do(func() {
			fmt.Fprintln(os.Stderr,
				"warn: daemon missing workflow.v1 capability — workflow mutations will fall back to standalone (slower). Restart the daemon to enable async writes.")
		})
	}
	c.capsMu.Unlock()
	return fresh[name]
}

// WorkflowMutate calls the daemon's WorkflowMutate method.
//
// Per the daemon contract:
//   - awaitDurability=false returns immediately with mode="daemon-async";
//     the daemon's drainer runs the mutation in the background. Caller is
//     expected to suppress non-correctness errors.
//   - awaitDurability=true blocks until the drainer finishes or the
//     daemon's per-call ceiling (lockTimeoutMs+2s) trips.
//
// The Client itself imposes no extra deadline beyond `c.timeout` (default
// 2s), which is INSUFFICIENT for sync writes that may queue behind 64
// jobs. Callers running sync should pass a ctx with a generous deadline
// (e.g. 30s) so the call() layer doesn't kill the connection mid-wait.
func (c *Client) WorkflowMutate(ctx context.Context, p WorkflowMutateParams) (WorkflowMutateResult, error) {
	var out WorkflowMutateResult
	if err := c.call(ctx, "WorkflowMutate", p, &out); err != nil {
		return WorkflowMutateResult{}, err
	}
	return out, nil
}

// Read calls the daemon's Read method.
func (c *Client) Read(ctx context.Context, p ReadParams) (ReadResult, error) {
	var out ReadResult
	if err := c.call(ctx, "Read", p, &out); err != nil {
		return ReadResult{}, err
	}
	return out, nil
}

// Track calls the daemon's Track method (best-effort).
func (c *Client) Track(ctx context.Context, p TrackParams) error {
	var ack map[string]any
	return c.call(ctx, "Track", p, &ack)
}

// SessionRegister calls the daemon's SessionRegister method.
func (c *Client) SessionRegister(ctx context.Context, p SessionRegisterParams) (SessionRegisterResult, error) {
	var out SessionRegisterResult
	if err := c.call(ctx, "SessionRegister", p, &out); err != nil {
		return SessionRegisterResult{}, err
	}
	return out, nil
}

// Shutdown asks the daemon to exit.
func (c *Client) Shutdown(ctx context.Context) error {
	var ack map[string]any
	return c.call(ctx, "Shutdown", struct{}{}, &ack)
}

func dialOnce(sockPath string) (net.Conn, error) {
	return net.DialTimeout("unix", sockPath, 200*time.Millisecond)
}

func (c *Client) call(ctx context.Context, method string, params, out any) error {
	conn, err := dialOnce(c.sockPath)
	if err != nil {
		return fmt.Errorf("dial daemon: %w", err)
	}
	defer func() { _ = conn.Close() }()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(c.timeout)
	}
	_ = conn.SetDeadline(deadline)

	id := c.id.Add(1)
	req := struct {
		JSONRPC string `json:"jsonrpc"`
		ID      int64  `json:"id"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", id, method, params}
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	rdr := bufio.NewReader(conn)
	line, err := rdr.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("rpc: %s", resp.Error.Message)
	}
	if out != nil {
		return json.Unmarshal(resp.Result, out)
	}
	return nil
}
