package commands

import (
	"bytes"
	"path/filepath"
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

func ptrStr(s string) *string { return &s }
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
