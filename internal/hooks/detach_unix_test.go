//go:build !windows

package hooks

import (
	"os/exec"
	"syscall"
	"testing"
)

func newDummyExecCmd() *exec.Cmd {
	return exec.Command("/usr/bin/true")
}

func assertDetachSysProcAttr(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.SysProcAttr == nil {
		t.Fatalf("ApplyDetachSysProcAttr did not set SysProcAttr on Unix")
	}
	attr, ok := any(cmd.SysProcAttr).(*syscall.SysProcAttr)
	if !ok || attr == nil {
		t.Fatalf("SysProcAttr is not *syscall.SysProcAttr on Unix: %T", cmd.SysProcAttr)
	}
	if !attr.Setpgid {
		t.Fatalf("Setpgid should be true on Unix; got false")
	}
}
