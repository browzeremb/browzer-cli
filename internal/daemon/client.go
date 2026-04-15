package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync/atomic"
	"time"
)

// Client is a thin JSON-RPC client over the daemon's Unix socket.
type Client struct {
	sockPath string
	id       atomic.Int64
	timeout  time.Duration
}

// NewClient returns a daemon client. Default timeout = 2s.
func NewClient(sockPath string) *Client {
	return &Client{sockPath: sockPath, timeout: 2 * time.Second}
}

// HealthResult mirrors the daemon's Health response.
type HealthResult struct {
	UptimeSec int    `json:"uptimeSec"`
	QueueLen  int    `json:"queueLen"`
	DBPath    string `json:"dbPath"`
}

// Health calls the daemon's Health method.
func (c *Client) Health(ctx context.Context) (HealthResult, error) {
	var out HealthResult
	if err := c.call(ctx, "Health", struct{}{}, &out); err != nil {
		return HealthResult{}, err
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
