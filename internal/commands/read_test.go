package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRead_PassthroughOnDaemonDown(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "foo.ts")
	body := "export function foo() { return 42; }\n"
	_ = os.WriteFile(src, []byte(body), 0o644)

	cmd := newReadCommand()
	cmd.SetArgs([]string{src, "--filter=none", "--daemon-socket", "/nonexistent/sock"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if stdout.String() != body {
		t.Fatalf("stdout = %q, want %q", stdout.String(), body)
	}
}

func TestRead_RangeForcesNone(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "foo.ts")
	body := "L1\nL2\nL3\nL4\nL5\n"
	_ = os.WriteFile(src, []byte(body), 0o644)

	cmd := newReadCommand()
	cmd.SetArgs([]string{src, "--filter=aggressive", "--offset=1", "--limit=2", "--daemon-socket", "/nonexistent/sock"})
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := stdout.String(); got != "L2\nL3\n" {
		t.Fatalf("stdout = %q, want %q", got, "L2\nL3\n")
	}
}
