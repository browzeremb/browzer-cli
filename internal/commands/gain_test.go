package commands

import (
	"bytes"
	"encoding/json"
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

	for i := 0; i < 5; i++ {
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
	for i := 0; i < 3; i++ {
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
	// F-007: when wastedTotal=0 the JSON `adoption` field is `null`
	// (parses as Go nil), normalizing on the API's shape. The human
	// renderer still prints "∞".
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
	for i := 0; i < 3; i++ {
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
	for i := 0; i < 2; i++ {
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
	// F-007 (AC-10): topWasted carries a `command` field paralleling the
	// human-readable cell.
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

// TestGain_ByMethod — `--by method` succeeds (groupColumn supports it
// after TASK_02). Sanity check the wiring through the gain command.
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

func ptrStr(s string) *string { return &s }
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
