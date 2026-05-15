package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/browzeremb/browzer-cli/internal/schema"
)

// TestWorkspaceShow_SchemaFlag drives `workspace show --schema --save <file>`
// end-to-end through the root cobra command and asserts:
//   - command execution returns no error
//   - the bytes written equal schema.WorkspaceShowSchemaJSON
//   - those bytes parse as valid JSON
//
// Using --save (rather than capturing os.Stdout) lets the test verify the
// full pipeline cleanly: cobra dispatch -> registerWorkspaceShow -> the
// schema.PrintOrSave call site preserved in schema.go.
func TestWorkspaceShow_SchemaFlag(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "workspace-show-schema.json")

	root := NewRootCommand("test")
	// `workspace show <id>` declares ExactArgs(1); pass a placeholder id
	// because cobra runs the args validator before RunE branches on --schema.
	// The placeholder is never consumed: --schema short-circuits to
	// schema.PrintOrSave before any network call.
	root.SetArgs([]string{"workspace", "show", "placeholder-ws", "--schema", "--save", outPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != schema.WorkspaceShowSchemaJSON {
		t.Errorf("written schema bytes do not match schema.WorkspaceShowSchemaJSON")
	}
	var v any
	if err := json.Unmarshal(got, &v); err != nil {
		t.Errorf("written schema does not parse as JSON: %v", err)
	}
}
