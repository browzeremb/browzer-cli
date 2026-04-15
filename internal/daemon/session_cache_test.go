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
