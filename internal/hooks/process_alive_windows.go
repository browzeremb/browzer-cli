//go:build windows

package hooks

import "golang.org/x/sys/windows"

// processAlive reports whether `pid` is still a live process on Windows.
//
// Strategy: open the process with PROCESS_QUERY_LIMITED_INFORMATION (the
// least-privileged access right that allows GetExitCodeProcess). If
// OpenProcess fails (e.g. ERROR_INVALID_PARAMETER when the PID has been
// recycled, or access-denied for system processes), we fall back
// conservatively:
//
//   - ERROR_INVALID_PARAMETER / not-found variants → process is dead → false.
//   - Any other OpenProcess error (e.g. access-denied for a live system
//     process we cannot query) → conservatively alive → true.
//
// When OpenProcess succeeds we call GetExitCodeProcess: STILL_ACTIVE (259)
// means the process is running; any other code means it has exited.
//
// Non-positive PIDs are classified as dead so a malformed lock file
// (e.g. "0") is reclaimable on every platform.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// ERROR_INVALID_PARAMETER is returned when the PID does not exist.
		// Treat it (and any "not found" class) as dead.
		if err == windows.ERROR_INVALID_PARAMETER {
			return false
		}
		// For other errors (e.g. access-denied querying a privileged system
		// process), assume the process is alive to avoid false reclamation.
		return true
	}
	defer windows.CloseHandle(h) //nolint:errcheck

	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		// Cannot determine exit code — assume alive.
		return true
	}
	const stillActive = 259 // STILL_ACTIVE — process has not exited
	return code == stillActive
}
