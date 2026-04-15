package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConfig_SetThenGet(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")

	cmd := newConfigCommand(func() string { return cfg })
	cmd.SetArgs([]string{"hook", "off"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("set: %v", err)
	}

	body, _ := os.ReadFile(cfg)
	var got map[string]any
	_ = json.Unmarshal(body, &got)
	if got["hook"] != "off" {
		t.Fatalf("hook = %v, want off", got["hook"])
	}

	cmd2 := newConfigCommand(func() string { return cfg })
	cmd2.SetArgs([]string{"hook"})
	var stdout bytes.Buffer
	cmd2.SetOut(&stdout)
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("get: %v", err)
	}
	if stdout.String() != "off\n" {
		t.Fatalf("stdout = %q, want off", stdout.String())
	}
}
