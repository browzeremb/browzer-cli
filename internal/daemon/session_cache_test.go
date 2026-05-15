package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionCache_RegisterAndLookup(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	body := `{"type":"session_start","model":"claude-opus-4-6"}` + "\n" +
		`{"type":"user_message","content":"hi"}` + "\n"
	_ = os.WriteFile(transcriptPath, []byte(body), 0o600)

	c := NewSessionCache(func(sid string) string { return filepath.Join(dir, sid+".json") })
	model, err := c.Register("sess_1", transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if model == nil || *model != "claude-opus-4-6" {
		t.Fatalf("model = %v, want claude-opus-4-6", model)
	}

	// Subsequent lookup hits the cache.
	model2, ok := c.Get("sess_1")
	if !ok || model2 == nil || *model2 != "claude-opus-4-6" {
		t.Fatal("Get should hit cache after Register")
	}
}

func TestSessionCache_NoModelFound(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	_ = os.WriteFile(transcriptPath, []byte(`{"type":"foo"}`+"\n"), 0o600)
	c := NewSessionCache(func(sid string) string { return filepath.Join(dir, sid+".json") })
	model, err := c.Register("sess_1", transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if model != nil {
		t.Fatalf("model should be nil when not found; got %v", *model)
	}
}

// TestSessionCache_NestedMessageModel pins the nested message.model
// path that the typed-row refactor must preserve byte-for-byte: when
// the top-level row has no `model` key but its `message` object does,
// extractModelFromTranscript MUST return it. Pre-refactor this lived
// in the map[string]any branch; post-refactor it lives in the typed
// transcriptRow.Message.Model field.
func TestSessionCache_NestedMessageModel(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	body := `{"type":"user_message","content":"hi"}` + "\n" +
		`{"type":"assistant_message","message":{"model":"claude-sonnet-4-6","role":"assistant"}}` + "\n"
	_ = os.WriteFile(transcriptPath, []byte(body), 0o600)

	c := NewSessionCache(func(sid string) string { return filepath.Join(dir, sid+".json") })
	model, err := c.Register("sess_nested", transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if model == nil || *model != "claude-sonnet-4-6" {
		t.Fatalf("nested model = %v, want claude-sonnet-4-6", model)
	}
}

// TestSessionCache_SkipFastIgnoresModellessLines pins FR-12 / AC-12: a
// transcript whose every line lacks the `"model"` substring still
// reaches the end-of-scan branch and returns (nil, nil) — proving the
// bytes.Contains skip-fast doesn't accidentally short-circuit the
// "no model found" case to a spurious hit.
func TestSessionCache_SkipFastIgnoresModellessLines(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "session.jsonl")
	body := `{"type":"hi"}` + "\n" + `{"type":"bye"}` + "\n"
	_ = os.WriteFile(transcriptPath, []byte(body), 0o600)

	c := NewSessionCache(func(sid string) string { return filepath.Join(dir, sid+".json") })
	model, err := c.Register("sess_empty", transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if model != nil {
		t.Fatalf("expected nil model on modelless transcript; got %v", *model)
	}
}
