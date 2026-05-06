package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// TestReadPayload_DashTreatedAsStdin pins the Unix `-` convention: when
// `--payload -` is passed, readPayload reads from stdin instead of treating
// `-` as a literal filename. Closes the doc-vs-CLI gap surfaced by the
// pipeline-phases.md cheat-sheet sample which documented `--payload -` long
// before the helper supported it.
func TestReadPayload_DashTreatedAsStdin(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	cmd.SetIn(bytes.NewBufferString(`{"hello":"world"}`))

	got, err := readPayload(cmd, "-")
	if err != nil {
		t.Fatalf("readPayload(-): unexpected error: %v", err)
	}
	if string(got) != `{"hello":"world"}` {
		t.Errorf("readPayload(-) returned %q, want %q", got, `{"hello":"world"}`)
	}
}

func TestReadPayload_EmptyFlagReadsStdin(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	cmd.SetIn(bytes.NewBufferString(`{"k":"v"}`))

	got, err := readPayload(cmd, "")
	if err != nil {
		t.Fatalf("readPayload(\"\"): unexpected error: %v", err)
	}
	if string(got) != `{"k":"v"}` {
		t.Errorf("readPayload(\"\") returned %q, want %q", got, `{"k":"v"}`)
	}
}

func TestReadPayload_NonDashPathReadsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "payload.json")
	if err := os.WriteFile(path, []byte(`{"file":true}`), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	cmd := &cobra.Command{Use: "test"}
	// Stdin must be ignored when a real file path is provided.
	cmd.SetIn(bytes.NewBufferString(`should-be-ignored`))

	got, err := readPayload(cmd, path)
	if err != nil {
		t.Fatalf("readPayload(path): unexpected error: %v", err)
	}
	if string(got) != `{"file":true}` {
		t.Errorf("readPayload(path) returned %q, want %q", got, `{"file":true}`)
	}
}
