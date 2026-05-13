package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/browzeremb/browzer-cli/internal/tracker"
)

func TestGain_RendersByModel(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	defer func() { _ = tr.Close() }()

	for range 5 {
		_ = tr.Record(tracker.Event{
			TS:          time.Now().UTC().Format(time.RFC3339),
			Source:      "hook-read", InputBytes: 1000, OutputBytes: 200, SavedTokens: 200, SavingsPct: 80,
			Model: ptrStr("claude-opus-4-6"),
		})
	}

	cmd := newGainCommand(func() string { return dbPath })
	cmd.SetArgs([]string{"--by", "model", "--since", "1d"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !contains(out, "claude-opus-4-6") || !contains(out, "5") {
		t.Fatalf("output missing expected fields: %q", out)
	}
}

func TestGain_UltraOneLine(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	defer func() { _ = tr.Close() }()
	_ = tr.Record(tracker.Event{
		TS:          time.Now().UTC().Format(time.RFC3339),
		Source:      "hook-read", InputBytes: 1000, OutputBytes: 200, SavedTokens: 200, SavingsPct: 80,
	})
	cmd := newGainCommand(func() string { return dbPath })
	Ultra = true
	defer func() { Ultra = false }()
	cmd.SetArgs([]string{})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	_ = cmd.Execute()
	if !contains(buf.String(), "saved") {
		t.Fatalf("ultra output missing 'saved': %q", buf.String())
	}
}

func TestGain_UltraShowsTopModel(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	defer func() { _ = tr.Close() }()
	for range 3 {
		_ = tr.Record(tracker.Event{
			TS:          time.Now().UTC().Format(time.RFC3339),
			Source:      "hook-read", InputBytes: 1000, OutputBytes: 200, SavedTokens: 200, SavingsPct: 80,
			Model: ptrStr("claude-opus-4-6"),
		})
	}
	cmd := newGainCommand(func() string { return dbPath })
	Ultra = true
	defer func() { Ultra = false }()
	cmd.SetArgs([]string{})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !contains(out, "top: claude-opus-4-6") {
		t.Fatalf("ultra output missing top model: %q", out)
	}
}

// TestAdoptionReport_NoData — empty DB renders an infinite ratio and
// zero totals.
func TestAdoptionReport_NoData(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	defer func() { _ = tr.Close() }()

	cmd := newGainCommand(func() string { return dbPath })
	cmd.SetArgs([]string{"--adoption", "--json", "--since", "1d"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, _ := payload["savedTotal"].(float64); v != 0 {
		t.Errorf("savedTotal = %v, want 0", v)
	}
	if v, _ := payload["wastedTotal"].(float64); v != 0 {
		t.Errorf("wastedTotal = %v, want 0", v)
	}
	// When wastedTotal=0 the JSON `adoption` field is `null` (parses as Go
	// nil), normalising on the API's shape. The human renderer still prints "∞".
	if raw, ok := payload["adoption"]; !ok {
		t.Errorf("adoption key missing")
	} else if raw != nil {
		t.Errorf("adoption = %v (%T), want null", raw, raw)
	}
}

// TestAdoptionReport_NoData_Human — empty DB renders ∞ in the table.
func TestAdoptionReport_NoData_Human(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	defer func() { _ = tr.Close() }()

	cmd := newGainCommand(func() string { return dbPath })
	cmd.SetArgs([]string{"--adoption", "--since", "1d"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(buf.String(), "∞") {
		t.Errorf("expected ∞ ratio in output, got: %q", buf.String())
	}
}

// TestAdoptionReport_OnlySaved — saved events but no wasted → ratio ∞.
func TestAdoptionReport_OnlySaved(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	defer func() { _ = tr.Close() }()
	for range 3 {
		_ = tr.Record(tracker.Event{
			TS:          time.Now().UTC().Format(time.RFC3339),
			Source:      "hook-cli-explore",
			InputBytes:  1000,
			OutputBytes: 200,
			SavedTokens: 500,
			SavingsPct:  80,
		})
	}
	cmd := newGainCommand(func() string { return dbPath })
	cmd.SetArgs([]string{"--adoption", "--since", "1d"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "∞") {
		t.Errorf("expected ∞ when wastedTotal=0, got: %q", out)
	}
	if !strings.Contains(out, "Saved tokens (CLI):       1500") {
		t.Errorf("expected savedFromCli=1500, got: %q", out)
	}
}

// TestAdoptionReport_BothSavedAndWasted — known mix yields exact ratio.
func TestAdoptionReport_BothSavedAndWasted(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	defer func() { _ = tr.Close() }()
	// 2 events saving 1000 each via cli-explore = 2000
	for range 2 {
		_ = tr.Record(tracker.Event{
			TS: time.Now().UTC().Format(time.RFC3339), Source: "hook-cli-explore",
			InputBytes: 4000, OutputBytes: 200, SavedTokens: 1000, SavingsPct: 80,
		})
	}
	// 1 wasted-grep with -200
	_ = tr.Record(tracker.Event{
		TS: time.Now().UTC().Format(time.RFC3339), Source: "wasted-grep",
		InputBytes: 800, OutputBytes: 0, SavedTokens: -200, SavingsPct: 0,
	})

	cmd := newGainCommand(func() string { return dbPath })
	cmd.SetArgs([]string{"--adoption", "--json", "--since", "1d"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	saved := payload["savedTotal"].(float64)
	wasted := payload["wastedTotal"].(float64)
	if saved != 2000 {
		t.Errorf("savedTotal = %v, want 2000", saved)
	}
	if wasted != 200 {
		t.Errorf("wastedTotal = %v, want 200", wasted)
	}
	ratio := payload["adoption"].(float64)
	if want := 10.0; math.Abs(ratio-want) > 1e-9 {
		t.Errorf("adoption ratio = %v, want %v", ratio, want)
	}
	tw, ok := payload["topWasted"].(map[string]any)
	if !ok {
		t.Fatalf("topWasted missing: %v", payload)
	}
	if tw["source"].(string) != "wasted-grep" {
		t.Errorf("topWasted.source = %v, want wasted-grep", tw["source"])
	}
	if tw["suggestion"].(string) != "browzer explore \"<query>\" --json --save /tmp/explore.json" {
		t.Errorf("topWasted.suggestion = %v", tw["suggestion"])
	}
	// topWasted carries a `command` field paralleling the human-readable cell.
	if cmd, _ := tw["command"].(string); cmd != "wasted-grep" {
		t.Errorf("topWasted.command = %v, want wasted-grep", tw["command"])
	}
}

// TestAdoptionReport_MutuallyExclusive — cobra rejects --adoption with --by.
func TestAdoptionReport_MutuallyExclusive(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	_ = tr.Close()

	cmd := newGainCommand(func() string { return dbPath })
	cmd.SetArgs([]string{"--adoption", "--by", "source"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("expected mutually-exclusive error, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") &&
		!strings.Contains(err.Error(), "none of the others") {
		t.Errorf("expected mutually-exclusive message, got: %v", err)
	}
}

// TestGain_ByMethod — `--by method` succeeds (groupColumn supports the method
// dimension). Sanity check the wiring through the gain command.
func TestGain_ByMethod(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	defer func() { _ = tr.Close() }()
	method := "measured"
	_ = tr.Record(tracker.Event{
		TS: time.Now().UTC().Format(time.RFC3339), Source: "hook-read",
		InputBytes: 1000, OutputBytes: 200, SavedTokens: 100, SavingsPct: 50,
		EstimationMethod: &method,
	})
	cmd := newGainCommand(func() string { return dbPath })
	cmd.SetArgs([]string{"--by", "method", "--json", "--since", "1d"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--by method failed: %v", err)
	}
	if !strings.Contains(buf.String(), "measured") {
		t.Errorf("output missing 'measured': %q", buf.String())
	}
}

// TestGain_WorkflowAuditExcludedByDefault checks that workflow-audit:* events
// are excluded when --include-internal is not set.
func TestGain_WorkflowAuditExcludedByDefault(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	defer func() { _ = tr.Close() }()
	// Insert a normal event and a workflow-audit event.
	_ = tr.Record(tracker.Event{
		TS: time.Now().UTC().Format(time.RFC3339), Source: "hook-read",
		InputBytes: 1000, OutputBytes: 200, SavedTokens: 100, SavingsPct: 50,
	})
	_ = tr.Record(tracker.Event{
		TS: time.Now().UTC().Format(time.RFC3339), Source: "workflow-audit:save-step",
		InputBytes: 500, OutputBytes: 0, SavedTokens: 0, SavingsPct: 0,
	})

	cmd := newGainCommand(func() string { return dbPath })
	cmd.SetArgs([]string{"--by", "source", "--json", "--since", "1d"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "workflow-audit") {
		t.Errorf("default output must not contain workflow-audit rows, got: %q", out)
	}
}

// TestGain_WorkflowAuditIncludedWithFlag checks that workflow-audit:* events
// appear when --include-internal is set.
func TestGain_WorkflowAuditIncludedWithFlag(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	defer func() { _ = tr.Close() }()
	_ = tr.Record(tracker.Event{
		TS: time.Now().UTC().Format(time.RFC3339), Source: "workflow-audit:save-step",
		InputBytes: 500, OutputBytes: 0, SavedTokens: 0, SavingsPct: 0,
	})

	cmd := newGainCommand(func() string { return dbPath })
	cmd.SetArgs([]string{"--by", "source", "--json", "--since", "1d", "--include-internal"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "workflow-audit") {
		t.Errorf("--include-internal output must contain workflow-audit rows, got: %q", out)
	}
}

// TestClassifyAdoptionSource_WastedRg checks that "wasted-rg" maps to "wasted".
func TestClassifyAdoptionSource_WastedRg(t *testing.T) {
	got := classifyAdoptionSource("wasted-rg")
	if got != "wasted" {
		t.Errorf("classifyAdoptionSource(%q) = %q, want %q", "wasted-rg", got, "wasted")
	}
}

// TestClassifyAdoptionSource_AllBuckets verifies every distinct input class
// maps to the correct bucket. This table kills boolean and return-value
// mutants on each case branch.
func TestClassifyAdoptionSource_AllBuckets(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		// savedFromCli via hook
		{"hook-cli-explore", "savedFromCli"},
		{"hook-cli-search", "savedFromCli"},
		{"hook-cli-deps", "savedFromCli"},
		{"hook-cli-ask", "savedFromCli"},
		// savedFromCli via bare "cli"
		{"cli", "savedFromCli"},
		// savedFromHooks
		{"hook-read", "savedFromHooks"},
		{"hook-grep-suggested", "savedFromHooks"},
		{"hook-glob-blocked", "savedFromHooks"},
		{"hook-glob-suggested", "savedFromHooks"},
		// wasted
		{"wasted-grep", "wasted"},
		{"wasted-rg", "wasted"},
		{"wasted-find", "wasted"},
		// other
		{"unknown-source", "other"},
		{"", "other"},
	}
	for _, tc := range cases {
		got := classifyAdoptionSource(tc.src)
		if got != tc.want {
			t.Errorf("classifyAdoptionSource(%q) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

// TestFormatRatio_Finite verifies that a finite ratio formats as "X.XX×".
func TestFormatRatio_Finite(t *testing.T) {
	got := formatRatio(10.0)
	if got != "10.00×" {
		t.Errorf("formatRatio(10.0) = %q, want %q", got, "10.00×")
	}
}

// TestFormatRatio_Infinity verifies that +Inf produces "∞".
func TestFormatRatio_Infinity(t *testing.T) {
	got := formatRatio(math.Inf(1))
	if got != "∞" {
		t.Errorf("formatRatio(+Inf) = %q, want ∞", got)
	}
}

// TestFormatRatio_DistinctFiniteAndInfinite ensures the two branches are
// not collapsed by a return-value mutant.
func TestFormatRatio_DistinctFiniteAndInfinite(t *testing.T) {
	fin := formatRatio(5.5)
	inf := formatRatio(math.Inf(1))
	if fin == inf {
		t.Errorf("formatRatio: finite and infinite must return different strings, both got %q", fin)
	}
}

// TestAbsInt64_Positive verifies that a positive value is returned unchanged.
func TestAbsInt64_Positive(t *testing.T) {
	if got := absInt64(42); got != 42 {
		t.Errorf("absInt64(42) = %d, want 42", got)
	}
}

// TestAbsInt64_Negative verifies that a negative value is negated.
func TestAbsInt64_Negative(t *testing.T) {
	if got := absInt64(-7); got != 7 {
		t.Errorf("absInt64(-7) = %d, want 7", got)
	}
}

// TestAbsInt64_Zero verifies that zero is returned unchanged.
func TestAbsInt64_Zero(t *testing.T) {
	if got := absInt64(0); got != 0 {
		t.Errorf("absInt64(0) = %d, want 0", got)
	}
}

// TestAbsInt64_DistinctNegAndPos ensures positive and negated-negative paths
// are not collapsed by a return-value mutant.
func TestAbsInt64_DistinctNegAndPos(t *testing.T) {
	a := absInt64(5)
	b := absInt64(-5)
	if a != b {
		t.Errorf("absInt64(5)=%d and absInt64(-5)=%d must be equal", a, b)
	}
	c := absInt64(3)
	d := absInt64(7)
	if c == d {
		t.Errorf("absInt64(3)=%d should not equal absInt64(7)=%d", c, d)
	}
}

// TestRenderGainUltra_SmallTotalIn — regression for divide-by-zero when
// 0 < totalIn < 4 (totalIn/4 truncates to 0 in integer arithmetic).
// Asserts that renderGainUltra does not panic for totalIn in {0,1,2,3,4,5}.
func TestRenderGainUltra_SmallTotalIn(t *testing.T) {
	for _, totalIn := range []int64{0, 1, 2, 3, 4, 5} {
		t.Run(fmt.Sprintf("totalIn=%d", totalIn), func(t *testing.T) {
			rows := []tracker.AggregatedRow{}
			if totalIn > 0 {
				rows = append(rows, tracker.AggregatedRow{
					Group: "hook-read", InputBytes: totalIn, SavedTokens: 1, N: 1,
				})
			}
			var buf bytes.Buffer
			if err := renderGainUltra(&buf, rows, "1d", nil); err != nil {
				t.Fatalf("renderGainUltra panicked or errored for totalIn=%d: %v", totalIn, err)
			}
		})
	}
}

// TestGain_HooksFlag_EmitsHooksSection verifies that `gain --hooks --json`
// emits a payload with a top-level "hooks" map alongside "rows".
func TestGain_HooksFlag_EmitsHooksSection(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	defer func() { _ = tr.Close() }()

	_ = tr.Record(tracker.Event{
		TS:          time.Now().UTC().Format(time.RFC3339),
		Source:      tracker.HookSavedTokens,
		Command:     "browzer-rewrite-read",
		InputBytes:  1000,
		OutputBytes: 200,
		SavedTokens: 150,
		SavingsPct:  75,
	})

	cmd := newGainCommand(func() string { return dbPath })
	cmd.SetArgs([]string{"--hooks", "--json", "--since", "1d"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := payload["hooks"]; !ok {
		t.Fatalf("hooks key missing from payload: %v", payload)
	}
	if _, ok := payload["rows"]; !ok {
		t.Fatalf("rows key missing from payload: %v", payload)
	}
	hooks, ok := payload["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks is not a map: %T", payload["hooks"])
	}
	delta, ok := hooks["browzer-rewrite-read"].(float64)
	if !ok {
		t.Fatalf("hooks[browzer-rewrite-read] missing or wrong type: %v", hooks)
	}
	if delta != 150 {
		t.Errorf("hooks[browzer-rewrite-read] = %v, want 150", delta)
	}
}

// TestGain_WithoutHooksFlag_ByteIdentical verifies that `gain --json` without
// --hooks produces output byte-identical to the baseline (a JSON array, not
// the hooks-augmented object).
func TestGain_WithoutHooksFlag_ByteIdentical(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	defer func() { _ = tr.Close() }()

	_ = tr.Record(tracker.Event{
		TS:          time.Now().UTC().Format(time.RFC3339),
		Source:      "hook-read",
		InputBytes:  1000,
		OutputBytes: 200,
		SavedTokens: 100,
		SavingsPct:  50,
	})

	// Capture baseline: --json only.
	baseline := func() string {
		c := newGainCommand(func() string { return dbPath })
		c.SetArgs([]string{"--json", "--since", "1d"})
		var b bytes.Buffer
		c.SetOut(&b)
		if err := c.Execute(); err != nil {
			t.Fatalf("baseline execute: %v", err)
		}
		return b.String()
	}()

	// Run again with the same args — must be identical.
	repeat := func() string {
		c := newGainCommand(func() string { return dbPath })
		c.SetArgs([]string{"--json", "--since", "1d"})
		var b bytes.Buffer
		c.SetOut(&b)
		if err := c.Execute(); err != nil {
			t.Fatalf("repeat execute: %v", err)
		}
		return b.String()
	}()

	if baseline != repeat {
		t.Errorf("--json output is not deterministic:\nbaseline: %q\nrepeat:   %q", baseline, repeat)
	}

	// Baseline must be a JSON array (not an object), proving --hooks was not applied.
	trimmed := strings.TrimSpace(baseline)
	if !strings.HasPrefix(trimmed, "[") {
		t.Errorf("--json without --hooks must produce a JSON array; got: %q", trimmed[:min(len(trimmed), 10)])
	}
}

// TestGain_HooksFlag_EmptyDB verifies that --hooks --json on an empty DB
// returns an object with empty hooks map and empty rows, not an error.
func TestGain_HooksFlag_EmptyDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	_ = tr.Close()

	cmd := newGainCommand(func() string { return dbPath })
	cmd.SetArgs([]string{"--hooks", "--json", "--since", "1d"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := payload["hooks"]; !ok {
		t.Fatalf("hooks key missing from empty-DB payload")
	}
	hooks := payload["hooks"].(map[string]any)
	if len(hooks) != 0 {
		t.Errorf("expected empty hooks map on empty DB, got: %v", hooks)
	}
}

// TestGain_SinceInvalid — `--since invalid` must return a user-friendly
// error that includes the bad value rather than a raw internal parse error.
func TestGain_SinceInvalid(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	_ = tr.Close()

	cmd := newGainCommand(func() string { return dbPath })
	cmd.SetArgs([]string{"--since", "invalid", "--json"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for --since invalid, got nil")
	}
	if !strings.Contains(err.Error(), "invalid") && !strings.Contains(err.Error(), "--since") {
		t.Errorf("error message should reference the bad value or flag, got: %v", err)
	}
}

// TestGain_HooksAloneEmitsJSON — `gain --hooks` without --json or --save
// must still emit a JSON payload (--hooks implies --json output format).
func TestGain_HooksAloneEmitsJSON(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	defer func() { _ = tr.Close() }()

	_ = tr.Record(tracker.Event{
		TS:          time.Now().UTC().Format(time.RFC3339),
		Source:      tracker.HookSavedTokens,
		Command:     "browzer-rewrite-read",
		InputBytes:  500,
		OutputBytes: 100,
		SavedTokens: 80,
		SavingsPct:  50,
	})

	cmd := newGainCommand(func() string { return dbPath })
	cmd.SetArgs([]string{"--hooks", "--since", "1d"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	// Must be valid JSON object, not a plain text table.
	var payload map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("--hooks alone must emit valid JSON; unmarshal failed: %v\noutput: %q", err, out)
	}
	if _, ok := payload["hooks"]; !ok {
		t.Errorf("hooks key missing from --hooks-alone output: %v", payload)
	}
}

// TestGain_HooksAndUltraMutuallyExclusive — cobra must reject --hooks --ultra.
func TestGain_HooksAndUltraMutuallyExclusive(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	_ = tr.Close()

	cmd := newGainCommand(func() string { return dbPath })
	cmd.SetArgs([]string{"--hooks", "--ultra"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected mutually-exclusive error for --hooks --ultra, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") &&
		!strings.Contains(err.Error(), "none of the others") {
		t.Errorf("expected mutually-exclusive message, got: %v", err)
	}
}

// TestGain_HooksAndAdoptionMutuallyExclusive — cobra must reject --hooks --adoption.
func TestGain_HooksAndAdoptionMutuallyExclusive(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	_ = tr.Close()

	cmd := newGainCommand(func() string { return dbPath })
	cmd.SetArgs([]string{"--hooks", "--adoption"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected mutually-exclusive error for --hooks --adoption, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") &&
		!strings.Contains(err.Error(), "none of the others") {
		t.Errorf("expected mutually-exclusive message, got: %v", err)
	}
}

// TestGain_HooksFlag_RowsNotNullOnEmptyDB — --hooks on an empty DB must
// emit "rows": [] (a JSON array), never "rows": null.
func TestGain_HooksFlag_RowsNotNullOnEmptyDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	_ = tr.Close()

	cmd := newGainCommand(func() string { return dbPath })
	cmd.SetArgs([]string{"--hooks", "--since", "1d"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Raw string check: must contain `"rows": []`, not `"rows": null`.
	out := buf.String()
	if !strings.Contains(out, `"rows"`) {
		t.Fatalf("rows key missing from output: %q", out)
	}
	if strings.Contains(out, `"rows": null`) || strings.Contains(out, `"rows":null`) {
		t.Errorf("rows must be [] not null on empty DB; got: %q", out)
	}
}

// TestGain_WithoutHooksFlag_GoldenShape verifies that `gain --json` without
// --hooks produces a JSON array (not an object), matching the baseline JSON
// shape from commit bc3a5832. The golden shape contract: top-level array of
// aggregated-row objects, each with at minimum "group", "n", "inputBytes",
// and "savedTokens" keys when rows are present.
func TestGain_WithoutHooksFlag_GoldenShape(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "h.db")
	tr, _ := tracker.Open(dbPath)
	defer func() { _ = tr.Close() }()

	_ = tr.Record(tracker.Event{
		TS:          time.Now().UTC().Format(time.RFC3339),
		Source:      "hook-read",
		InputBytes:  2000,
		OutputBytes: 400,
		SavedTokens: 300,
		SavingsPct:  60,
	})

	cmd := newGainCommand(func() string { return dbPath })
	cmd.SetArgs([]string{"--json", "--since", "1d"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	trimmed := strings.TrimSpace(buf.String())

	// Shape gate 1: must be a JSON array, not an object.
	if !strings.HasPrefix(trimmed, "[") {
		t.Errorf("--json without --hooks must produce a JSON array; got prefix: %q", trimmed[:min(len(trimmed), 20)])
	}

	// Shape gate 2: parse and verify required fields on each row.
	// Field names use Go capitalized form (no json tags on AggregatedRow).
	var rows []map[string]any
	if err := json.Unmarshal([]byte(trimmed), &rows); err != nil {
		t.Fatalf("unmarshal JSON array: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one row")
	}
	required := []string{"Group", "N", "InputBytes", "SavedTokens"}
	for _, key := range required {
		if _, ok := rows[0][key]; !ok {
			t.Errorf("row missing required field %q; row: %v", key, rows[0])
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func ptrStr(s string) *string { return &s }
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
