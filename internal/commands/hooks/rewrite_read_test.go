package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
)

// mustMarshalReadInput serialises a PreToolUse(Read) hook input envelope for
// use in test cases.
func mustMarshalReadInput(t *testing.T, toolName, filePath string, offset, limit *int) string {
	t.Helper()
	ti := map[string]any{"file_path": filePath}
	if offset != nil {
		ti["offset"] = *offset
	}
	if limit != nil {
		ti["limit"] = *limit
	}
	in := map[string]any{
		"tool_name":  toolName,
		"tool_input": ti,
		"session_id": "test",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// intPtr is a tiny helper that returns a pointer to an int literal.
func intPtr(v int) *int { return &v }

// runRewriteRead drives rewriteReadDecide and decodes the advisory
// envelope when one is emitted.
func runRewriteReadDecide(t *testing.T, raw string) (int, *rewriteEnvelope) {
	t.Helper()
	code, env := rewriteReadDecide([]byte(raw))
	return code, env
}

// writeFixture writes count bytes of the given byte pattern to a temp file
// and returns its absolute path.
func writeFixture(t *testing.T, dir string, name string, size int, pattern byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	data := bytes.Repeat([]byte{pattern}, size)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
	return p
}

// writeTextFixture writes count newline-separated lines of ASCII 'x' to a
// temp file and returns its absolute path.
func writeTextFixture(t *testing.T, dir, name string, size int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	// Fill with printable ASCII so binary detection returns false.
	data := bytes.Repeat([]byte("x\n"), size/2+1)
	if err := os.WriteFile(p, data[:size], 0o600); err != nil {
		t.Fatalf("write text fixture %s: %v", name, err)
	}
	return p
}

// writeBinaryFixture writes a file whose null-byte prevalence exceeds
// binaryThreshold. Returns its absolute path.
func writeBinaryFixture(t *testing.T, dir, name string, size int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	data := make([]byte, size)
	// Seed ~1% null bytes — well above the 0.5% binaryThreshold.
	for i := 0; i < size; i += 100 {
		data[i] = 0
	}
	// Fill remaining bytes with printable ASCII.
	for i := range size {
		if data[i] == 0 && (i%100 != 0) {
			data[i] = 'x'
		}
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatalf("write binary fixture %s: %v", name, err)
	}
	return p
}

func TestRewriteRead_TableDriven(t *testing.T) {
	wsRoot := setupWorkspaceFixture(t)
	tmp := t.TempDir()

	// Prepare fixture files.
	smallFile := writeTextFixture(t, tmp, "small.ts", 10*1024)       // 10 KB → passthrough
	mediumFile := writeTextFixture(t, tmp, "medium.ts", 80*1024)      // 80 KB → advisory only
	largeFile := writeTextFixture(t, tmp, "large.ts", 200*1024)       // 200 KB → advisory + head
	binaryFile := writeBinaryFixture(t, tmp, "large.bin", 200*1024)   // 200 KB binary → advisory + binary:true
	envFile := writeFixture(t, tmp, ".env.local", 1*1024, 'x')        // NeverRewrite match
	_ = wsRoot

	cases := []struct {
		name         string
		toolName     string
		filePath     string
		offset       *int
		limit        *int
		wantCode     int
		wantEnvelope bool
		wantHead     bool
		wantBinary   bool
	}{
		{
			name:         "small file → passthrough",
			toolName:     "Read",
			filePath:     smallFile,
			wantCode:     internalhooks.ExitPassthrough,
			wantEnvelope: false,
		},
		{
			name:         "medium file (40–160KB) → advisory only (no head)",
			toolName:     "Read",
			filePath:     mediumFile,
			wantCode:     internalhooks.ExitAllow,
			wantEnvelope: true,
			wantHead:     false,
			wantBinary:   false,
		},
		{
			name:         "large file (>160KB) text → advisory + head in summary",
			toolName:     "Read",
			filePath:     largeFile,
			wantCode:     internalhooks.ExitAllow,
			wantEnvelope: true,
			wantHead:     true,
			wantBinary:   false,
		},
		{
			name:         "binary file (>160KB) → advisory + binary:true (no head)",
			toolName:     "Read",
			filePath:     binaryFile,
			wantCode:     internalhooks.ExitAllow,
			wantEnvelope: true,
			wantHead:     false,
			wantBinary:   true,
		},
		{
			name:         "NeverRewrite match (.env) → passthrough",
			toolName:     "Read",
			filePath:     envFile,
			wantCode:     internalhooks.ExitPassthrough,
			wantEnvelope: false,
		},
		{
			name:         "offset set → passthrough",
			toolName:     "Read",
			filePath:     largeFile,
			offset:       intPtr(10),
			wantCode:     internalhooks.ExitPassthrough,
			wantEnvelope: false,
		},
		{
			name:         "limit set → passthrough",
			toolName:     "Read",
			filePath:     largeFile,
			limit:        intPtr(50),
			wantCode:     internalhooks.ExitPassthrough,
			wantEnvelope: false,
		},
		{
			name:         "tool_name != Read → passthrough",
			toolName:     "Glob",
			filePath:     largeFile,
			wantCode:     internalhooks.ExitPassthrough,
			wantEnvelope: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := mustMarshalReadInput(t, tc.toolName, tc.filePath, tc.offset, tc.limit)
			code, env := runRewriteReadDecide(t, raw)

			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d", code, tc.wantCode)
			}

			if !tc.wantEnvelope {
				if env != nil {
					t.Errorf("no envelope expected, got %+v", env)
				}
				return
			}

			if env == nil || env.HookSpecificOutput == nil {
				t.Fatalf("expected advisory envelope, got nil")
			}

			hso := env.HookSpecificOutput
			if hso.HookEventName != "PreToolUse" {
				t.Errorf("hookEventName = %q, want PreToolUse", hso.HookEventName)
			}
			if hso.PermissionDecision != "allow" {
				t.Errorf("permissionDecision = %q, want allow", hso.PermissionDecision)
			}
			if hso.AdditionalContext == "" {
				t.Error("additionalContext must be non-empty")
			}

			// Decode the embedded JSON summary block from additionalContext.
			summary := extractJSONSummary(t, hso.AdditionalContext)

			if summary["path"] != tc.filePath {
				t.Errorf("summary path = %q, want %q", summary["path"], tc.filePath)
			}
			if _, ok := summary["byteCount"]; !ok {
				t.Error("summary missing byteCount")
			}

			if tc.wantHead {
				if _, hasHead := summary["head"]; !hasHead {
					t.Errorf("expected 'head' in summary, got %v", summary)
				}
			} else {
				if _, hasHead := summary["head"]; hasHead {
					t.Errorf("unexpected 'head' in summary for non-head case")
				}
			}

			if tc.wantBinary {
				v, ok := summary["binary"]
				if !ok {
					t.Errorf("expected 'binary' in summary, got %v", summary)
				} else if v != true {
					t.Errorf("binary = %v, want true", v)
				}
			} else {
				if _, hasBinary := summary["binary"]; hasBinary {
					t.Errorf("unexpected 'binary' in summary for non-binary case")
				}
			}
		})
	}
}

// extractJSONSummary parses the first ` ```json … ``` ` fenced block from
// additionalContext and returns the decoded map.
func extractJSONSummary(t *testing.T, ctx string) map[string]any {
	t.Helper()
	const open = "```json\n"
	const close = "\n```"

	start := strings.Index(ctx, open)
	if start < 0 {
		t.Fatalf("no ```json block in additionalContext:\n%s", ctx)
	}
	start += len(open)

	end := strings.Index(ctx[start:], close)
	if end < 0 {
		t.Fatalf("unclosed ```json block in additionalContext:\n%s", ctx)
	}
	raw := ctx[start : start+end]

	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decode summary JSON %q: %v", raw, err)
	}
	return m
}

func TestRewriteRead_GateDisabled_Passthrough(t *testing.T) {
	setupWorkspaceFixture(t)
	t.Setenv("BROWZER_HOOK", "off")
	tmp := t.TempDir()
	largeFile := writeTextFixture(t, tmp, "large.ts", 200*1024)
	raw := mustMarshalReadInput(t, "Read", largeFile, nil, nil)
	code, env := runRewriteReadDecide(t, raw)
	if code != internalhooks.ExitPassthrough {
		t.Errorf("gate off: exit code = %d, want %d", code, internalhooks.ExitPassthrough)
	}
	if env != nil {
		t.Errorf("gate off: envelope should be nil")
	}
}

func TestRewriteRead_AdvisoryContainsFileSize(t *testing.T) {
	setupWorkspaceFixture(t)
	tmp := t.TempDir()
	// Write exactly 80 KB so ~80KB appears in the advisory.
	mediumFile := writeTextFixture(t, tmp, "medium.go", 80*1024)
	raw := mustMarshalReadInput(t, "Read", mediumFile, nil, nil)
	code, env := runRewriteReadDecide(t, raw)
	if code != internalhooks.ExitAllow {
		t.Fatalf("exit code = %d, want %d (allow)", code, internalhooks.ExitAllow)
	}
	if env == nil || env.HookSpecificOutput == nil {
		t.Fatal("expected advisory envelope")
	}
	ctx := env.HookSpecificOutput.AdditionalContext
	if !strings.Contains(ctx, "KB") {
		t.Errorf("advisory missing KB size annotation: %s", ctx)
	}
	if !strings.Contains(ctx, "browzer explore") {
		t.Errorf("advisory missing browzer explore suggestion: %s", ctx)
	}
}

func TestRewriteRead_LargeFileAdvisoryLabel(t *testing.T) {
	setupWorkspaceFixture(t)
	tmp := t.TempDir()
	largeFile := writeTextFixture(t, tmp, "large.go", 200*1024)
	raw := mustMarshalReadInput(t, "Read", largeFile, nil, nil)
	_, env := runRewriteReadDecide(t, raw)
	if env == nil || env.HookSpecificOutput == nil {
		t.Fatal("expected envelope")
	}
	ctx := env.HookSpecificOutput.AdditionalContext
	wantLabel := "first 60 lines (5KB cap)"
	if !strings.Contains(ctx, wantLabel) {
		t.Errorf("expected label %q in advisory, got:\n%s", wantLabel, ctx)
	}
}

func TestRewriteRead_MediumFileAdvisoryLabel(t *testing.T) {
	setupWorkspaceFixture(t)
	tmp := t.TempDir()
	mediumFile := writeTextFixture(t, tmp, "medium.go", 80*1024)
	raw := mustMarshalReadInput(t, "Read", mediumFile, nil, nil)
	_, env := runRewriteReadDecide(t, raw)
	if env == nil || env.HookSpecificOutput == nil {
		t.Fatal("expected envelope")
	}
	ctx := env.HookSpecificOutput.AdditionalContext
	wantLabel := "advisory-only — head skipped to preserve token economy"
	if !strings.Contains(ctx, wantLabel) {
		t.Errorf("expected label %q in advisory, got:\n%s", wantLabel, ctx)
	}
}

func TestRewriteRead_BinaryFileAdvisoryLabel(t *testing.T) {
	setupWorkspaceFixture(t)
	tmp := t.TempDir()
	binaryFile := writeBinaryFixture(t, tmp, "large.bin", 200*1024)
	raw := mustMarshalReadInput(t, "Read", binaryFile, nil, nil)
	_, env := runRewriteReadDecide(t, raw)
	if env == nil || env.HookSpecificOutput == nil {
		t.Fatal("expected envelope")
	}
	ctx := env.HookSpecificOutput.AdditionalContext
	wantLabel := "binary file — head omitted"
	if !strings.Contains(ctx, wantLabel) {
		t.Errorf("expected label %q in advisory, got:\n%s", wantLabel, ctx)
	}
}
