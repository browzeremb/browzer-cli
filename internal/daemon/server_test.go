package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestServer_RespondsToHealth(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")

	srv := NewServer(Options{SocketPath: sockPath})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	defer srv.Stop()

	// Wait for socket to exist.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	req := `{"jsonrpc":"2.0","id":1,"method":"Health","params":{}}` + "\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}

	rdr := bufio.NewReader(conn)
	line, err := rdr.ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp struct {
		Result struct {
			UptimeSec int    `json:"uptimeSec"`
			DBPath    string `json:"dbPath"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}
	if resp.Result.UptimeSec < 0 {
		t.Fatalf("UptimeSec must be >=0, got %d", resp.Result.UptimeSec)
	}
}

// TestHealthCapabilities_OmitWorkflowMutateSurface is the integration pin
// for the workflow-mutate RPC retirement: spin up a real daemon over a
// Unix socket, call Health via the client, and assert the advertised
// capabilities array contains neither `workflow.v1` nor `workflow.fsync.v1`.
// The retained baseline capabilities (`read.v1`, `track.v1`,
// `session-register.v1`) are spot-checked to defend against accidental
// over-removal in future refactors.
func TestHealthCapabilities_OmitWorkflowMutateSurface(t *testing.T) {
	ed := SpinUpEphemeralDaemon(t)

	h, err := ed.Client.Health(t.Context())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}

	for _, retired := range []string{"workflow.v1", "workflow.fsync.v1"} {
		if slices.Contains(h.Capabilities, retired) {
			t.Errorf("capabilities advertise retired %q: %v", retired, h.Capabilities)
		}
	}
	for _, retained := range []string{"read.v1", "track.v1", "session-register.v1"} {
		if !slices.Contains(h.Capabilities, retained) {
			t.Errorf("capabilities missing retained %q: %v", retained, h.Capabilities)
		}
	}
}

// TestHandshakeRejection_WorkflowMutate_MethodNotFound is the regression pin
// for the v5.0.0 WorkflowMutate RPC retirement at the dispatch level. A v2-pinned
// client that sends a raw `WorkflowMutate` JSON-RPC request MUST receive an error
// response with code -32601 and a message that contains both "method_not_found"
// and "WorkflowMutate" — i.e. the handleConn.lookupHandler() fast-path fires and
// the socket is not silently dropped. This is the counterpart to
// TestProtocolFeatures_OmitsRetiredWorkflowFeatures (which only checks the
// constant arrays) and TestHealthCapabilities_OmitWorkflowMutateSurface (which
// only checks the Health advertised capabilities slice).
func TestHandshakeRejection_WorkflowMutate_MethodNotFound(t *testing.T) {
	ed := SpinUpEphemeralDaemon(t)

	conn, err := net.Dial("unix", ed.SocketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	req := `{"jsonrpc":"2.0","method":"WorkflowMutate","id":42,"params":{}}` + "\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}

	rdr := bufio.NewReader(conn)
	line, err := rdr.ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var resp struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}
	if resp.Error == nil {
		t.Fatalf("expected error response for retired method WorkflowMutate, got: %s", line)
	}
	if resp.Error.Code != -32601 {
		t.Errorf("error.code = %d, want -32601", resp.Error.Code)
	}
	if !strings.Contains(resp.Error.Message, "method_not_found") {
		t.Errorf("error.message %q does not contain %q", resp.Error.Message, "method_not_found")
	}
	if !strings.Contains(resp.Error.Message, "WorkflowMutate") {
		t.Errorf("error.message %q does not contain %q", resp.Error.Message, "WorkflowMutate")
	}
}

// TestHandlerError_CodedErrorInterface verifies that a handler returning an
// error implementing interface{ Code() int } propagates the custom JSON-RPC
// code to the wire response instead of collapsing to -32000.
func TestHandlerError_CodedErrorInterface(t *testing.T) {
	sockDir, err := os.MkdirTemp("/tmp", "brz-coded-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sockPath := filepath.Join(sockDir, "coded.sock")

	srv := NewServer(Options{SocketPath: sockPath})
	// Register a stub handler that returns a coded error (-32602 invalid_params)
	// before Serve starts so the handler is present when the first request arrives.
	srv.RegisterHandler("TestCoded", func(_ context.Context, _ json.RawMessage) (any, error) {
		return nil, &codedTestError{code: -32602, msg: "invalid_params: missing field"}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	defer srv.Stop()

	// Wait for the socket file to appear (Serve binds before chmod).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, dialErr := net.Dial("unix", sockPath); dialErr == nil {
			_ = c.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	req := `{"jsonrpc":"2.0","id":9,"method":"TestCoded","params":{}}` + "\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}

	rdr := bufio.NewReader(conn)
	line, err := rdr.ReadString('\n')
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var resp struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}
	if resp.Error == nil {
		t.Fatalf("expected error response, got: %s", line)
	}
	if resp.Error.Code != -32602 {
		t.Errorf("error.code = %d, want -32602 (invalid_params)", resp.Error.Code)
	}
	if !strings.Contains(resp.Error.Message, "invalid_params") {
		t.Errorf("error.message %q does not contain %q", resp.Error.Message, "invalid_params")
	}
}

// codedTestError is a test-only error type that implements interface{ Code() int }
// so TestHandlerError_CodedErrorInterface can exercise the handleConn opt-in path.
type codedTestError struct {
	code int
	msg  string
}

func (e *codedTestError) Error() string { return e.msg }
func (e *codedTestError) Code() int     { return e.code }

func TestServer_SocketHasMode0600(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "test.sock")
	srv := NewServer(Options{SocketPath: sockPath})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	defer srv.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	info, err := os.Stat(sockPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket mode = %o, want 0600", perm)
	}
}
