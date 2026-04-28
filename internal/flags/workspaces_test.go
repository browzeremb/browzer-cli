package flags_test

// RED tests for ParseWorkspacesFlag and mutual-exclusion validation.
// (T-03-08, T-03-09)
//
// Expected failure: "undefined: flags.ParseWorkspacesFlag" because
// packages/cli/internal/flags/workspaces.go does not exist yet.

import (
	"testing"

	"github.com/browzeremb/browzer-cli/internal/flags"
)

// T-03-08: ParseWorkspacesFlag parses comma-separated workspace IDs.

func TestParseWorkspacesFlag_CommaSeparated(t *testing.T) {
	got, err := flags.ParseWorkspacesFlag("id1,id2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 workspace IDs, got %d: %v", len(got), got)
	}
	if got[0] != "id1" {
		t.Errorf("expected got[0]='id1', got %q", got[0])
	}
	if got[1] != "id2" {
		t.Errorf("expected got[1]='id2', got %q", got[1])
	}
}

func TestParseWorkspacesFlag_Single(t *testing.T) {
	got, err := flags.ParseWorkspacesFlag("only-one")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 workspace ID, got %d", len(got))
	}
	if got[0] != "only-one" {
		t.Errorf("expected 'only-one', got %q", got[0])
	}
}

func TestParseWorkspacesFlag_EmptyString_ReturnsEmptyNoError(t *testing.T) {
	got, err := flags.ParseWorkspacesFlag("")
	if err != nil {
		t.Fatalf("empty string must return nil error, got: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty string must return empty slice, got %v", got)
	}
}

func TestParseWorkspacesFlag_TrimsWhitespace(t *testing.T) {
	got, err := flags.ParseWorkspacesFlag(" id1 , id2 ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 entries after trim, got %d", len(got))
	}
	if got[0] != "id1" {
		t.Errorf("expected 'id1' after trim, got %q", got[0])
	}
	if got[1] != "id2" {
		t.Errorf("expected 'id2' after trim, got %q", got[1])
	}
}

// T-03-09: mutual exclusion — --workspaces and --all-workspaces cannot both be set.

func TestValidateWorkspaceFlags_AllWorkspacesAloneIsValid(t *testing.T) {
	err := flags.ValidateWorkspaceFlags(nil, true)
	if err != nil {
		t.Errorf("--all-workspaces alone must be valid, got error: %v", err)
	}
}

func TestValidateWorkspaceFlags_WorkspacesAloneIsValid(t *testing.T) {
	err := flags.ValidateWorkspaceFlags([]string{"ws-1", "ws-2"}, false)
	if err != nil {
		t.Errorf("--workspaces alone must be valid, got error: %v", err)
	}
}

func TestValidateWorkspaceFlags_NeitherIsValid(t *testing.T) {
	err := flags.ValidateWorkspaceFlags(nil, false)
	if err != nil {
		t.Errorf("neither flag is valid (single-workspace mode), got error: %v", err)
	}
}

func TestValidateWorkspaceFlags_BothSetReturnsError(t *testing.T) {
	// RED: ValidateWorkspaceFlags does not exist yet
	err := flags.ValidateWorkspaceFlags([]string{"ws-1"}, true)
	if err == nil {
		t.Fatal("expected error when both --workspaces and --all-workspaces are set, got nil")
	}
}

func TestValidateWorkspaceFlags_EmptyWorkspacesSliceWithAllWorkspaces(t *testing.T) {
	// Edge case: explicitly passing an empty workspaces slice + all-workspaces=true
	// should still be treated as mutual exclusion conflict only if slice is non-nil.
	// Empty (nil) slice + all-workspaces=true is fine.
	err := flags.ValidateWorkspaceFlags([]string{}, true)
	// Empty explicit slice is treated as "no workspaces specified"
	if err != nil {
		t.Errorf("empty workspaces slice + all-workspaces should not conflict, got: %v", err)
	}
}

// Table-driven test for ParseWorkspacesFlag edge cases.

func TestParseWorkspacesFlag_Table(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantLen  int
		wantErr  bool
	}{
		{"two IDs", "abc,def", 2, false},
		{"three IDs", "x,y,z", 3, false},
		{"empty", "", 0, false},
		{"single no comma", "only", 1, false},
		{"trailing comma", "a,b,", 2, false}, // trailing empty token is ignored
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := flags.ParseWorkspacesFlag(tc.input)
			if tc.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if len(got) != tc.wantLen {
				t.Errorf("expected %d IDs, got %d: %v", tc.wantLen, len(got), got)
			}
		})
	}
}
