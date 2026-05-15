package hooks

import (
	"encoding/json"
	"testing"
)

// TestClassifyWasted_rgRoutesToWastedRg mirrors the JS test:
// "rg command routes to wasted-rg"
func TestClassifyWasted_rgRoutesToWastedRg(t *testing.T) {
	cases := []string{
		"rg foo .",
		"rg --hidden pattern src/",
		"  rg somepattern",
	}
	for _, c := range cases {
		if got := classifyWasted(c); got != "wasted-rg" {
			t.Errorf("classifyWasted(%q) = %q; want wasted-rg", c, got)
		}
	}
}

// TestClassifyWasted_grepRRoutesToWastedGrep mirrors the JS test:
// "grep -r routes to wasted-grep (no regression)"
func TestClassifyWasted_grepRRoutesToWastedGrep(t *testing.T) {
	cases := []string{
		"grep -r pattern .",
		"grep pattern -r .",
	}
	for _, c := range cases {
		if got := classifyWasted(c); got != "wasted-grep" {
			t.Errorf("classifyWasted(%q) = %q; want wasted-grep", c, got)
		}
	}
}

// TestClassifyWasted_grepCapRRoutesToWastedGrep mirrors the JS test:
// "grep -R routes to wasted-grep (no regression)"
func TestClassifyWasted_grepCapRRoutesToWastedGrep(t *testing.T) {
	if got := classifyWasted("grep -R pattern ."); got != "wasted-grep" {
		t.Errorf("classifyWasted(grep -R) = %q; want wasted-grep", got)
	}
}

// TestClassifyWasted_findNameRoutesToWastedFind mirrors the JS test:
// "find -name routes to wasted-find"
func TestClassifyWasted_findNameRoutesToWastedFind(t *testing.T) {
	if got := classifyWasted(`find . -name "*.ts"`); got != "wasted-find" {
		t.Errorf("classifyWasted(find -name) = %q; want wasted-find", got)
	}
}

// TestClassifyWasted_unrelatedCommandReturnsEmpty mirrors the JS test:
// "unrelated command returns null"
func TestClassifyWasted_unrelatedCommandReturnsEmpty(t *testing.T) {
	cases := []string{
		"ls -la",
		"cat file.txt",
		"browzer explore foo",
	}
	for _, c := range cases {
		if got := classifyWasted(c); got != "" {
			t.Errorf("classifyWasted(%q) = %q; want empty", c, got)
		}
	}
}

// TestTrackWastedDecide_emptyInputReturnsEmpty verifies that a zero-length
// stdin produces no telemetry and does not panic.
func TestTrackWastedDecide_emptyInputReturnsEmpty(t *testing.T) {
	setupWorkspaceFixture(t)
	got := trackWastedDecide([]byte{})
	if got != "" {
		t.Errorf("trackWastedDecide(empty) = %q; want empty", got)
	}
}

// TestTrackWastedDecide_wrongToolNameReturnsEmpty verifies that non-Bash
// tool names are ignored.
func TestTrackWastedDecide_wrongToolNameReturnsEmpty(t *testing.T) {
	setupWorkspaceFixture(t)
	payload, _ := json.Marshal(map[string]any{
		"tool_name":     "Read",
		"tool_input":    map[string]any{"command": "rg foo ."},
		"tool_response": map[string]any{"stdout": "some output"},
		"session_id":    "sess-1",
	})
	got := trackWastedDecide(payload)
	if got != "" {
		t.Errorf("trackWastedDecide(Read tool) = %q; want empty", got)
	}
}

// TestTrackWastedDecide_rgCommandEmitsWastedRg verifies the full decide
// path for a Bash rg command.
func TestTrackWastedDecide_rgCommandEmitsWastedRg(t *testing.T) {
	setupWorkspaceFixture(t)
	payload, _ := json.Marshal(map[string]any{
		"tool_name":     "Bash",
		"tool_input":    map[string]any{"command": "rg --fixed-strings TODO src/"},
		"tool_response": map[string]any{"stdout": "src/foo.ts:42: TODO fix this"},
		"session_id":    "sess-wasted-1",
	})
	got := trackWastedDecide(payload)
	if got != "wasted-rg" {
		t.Errorf("trackWastedDecide(rg) = %q; want wasted-rg", got)
	}
}

// TestTrackWastedDecide_browzerCommandIgnored verifies that a browzer
// command is not classified as wasted (those are tracked by track-cli).
func TestTrackWastedDecide_browzerCommandIgnored(t *testing.T) {
	setupWorkspaceFixture(t)
	payload, _ := json.Marshal(map[string]any{
		"tool_name":     "Bash",
		"tool_input":    map[string]any{"command": "browzer explore query"},
		"tool_response": map[string]any{"stdout": "{}"},
		"session_id":    "sess-2",
	})
	got := trackWastedDecide(payload)
	if got != "" {
		t.Errorf("trackWastedDecide(browzer explore) = %q; want empty", got)
	}
}
