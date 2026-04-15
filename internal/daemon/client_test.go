package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestClient_HealthRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "test.sock")
	srv := NewServer(Options{SocketPath: sock, DBPath: "/tmp/foo.db"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	defer srv.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := dialOnce(sock); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	c := NewClient(sock)
	res, err := c.Health(context.Background())
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if res.DBPath != "/tmp/foo.db" {
		t.Fatalf("DBPath = %q", res.DBPath)
	}
}
