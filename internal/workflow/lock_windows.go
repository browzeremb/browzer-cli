//go:build windows

package workflow

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// acquireExclusive attempts a non-blocking exclusive lock via LockFileEx on
// the given file descriptor (handle).
func acquireExclusive(fd uintptr) error {
	const lockfileExclusiveLock = 0x00000002
	const lockfileFailImmediately = 0x00000001
	ol := new(windows.Overlapped)
	err := windows.LockFileEx(
		windows.Handle(fd),
		lockfileExclusiveLock|lockfileFailImmediately,
		0,
		1,
		0,
		ol,
	)
	if err != nil {
		return fmt.Errorf("LockFileEx: %w", err)
	}
	return nil
}

// releaseExclusive releases a lock held via LockFileEx.
func releaseExclusive(fd uintptr) error {
	ol := new(windows.Overlapped)
	// Suppress "unsafe.Pointer of uintptr" vet warning — windows.UnlockFileEx
	// requires an *Overlapped; the value is only read inside the syscall.
	err := windows.UnlockFileEx(
		windows.Handle(fd),
		0,
		1,
		0,
		(*windows.Overlapped)(unsafe.Pointer(ol)),
	)
	if err != nil {
		return fmt.Errorf("UnlockFileEx: %w", err)
	}
	return nil
}

// pidIsAlive reports whether the process with the given PID is still running
// on Windows by attempting to open it with SYNCHRONIZE rights.
func pidIsAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	windows.CloseHandle(h) //nolint:errcheck
	// Check exit code to distinguish a zombie handle from an alive process.
	var code uint32
	h2, err2 := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err2 != nil {
		return true // opened with SYNCHRONIZE, assume alive
	}
	defer windows.CloseHandle(h2) //nolint:errcheck
	if err := windows.GetExitCodeProcess(h2, &code); err != nil {
		return true
	}
	return code == 259 // STILL_ACTIVE
}

// ensure os import is used (for consistency; not actually needed here).
var _ = os.Getpid
