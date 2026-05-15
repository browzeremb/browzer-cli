//go:build windows

package hooks

import (
	"os/exec"
	"testing"
)

func newDummyExecCmd() *exec.Cmd {
	return exec.Command("cmd", "/c", "exit 0")
}

func assertDetachSysProcAttr(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	// On Windows the helper is intentionally a no-op (the Browzer
	// daemon is not a primary Windows target). Asserting only that
	// the helper does not panic is sufficient — leaving SysProcAttr
	// nil is the contract.
	if cmd.SysProcAttr != nil {
		t.Fatalf("ApplyDetachSysProcAttr on Windows should be a no-op; got %+v", cmd.SysProcAttr)
	}
}
