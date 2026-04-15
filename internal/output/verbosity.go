package output

import (
	"fmt"
	"io"
	"os"
)

// Verbose holds the global verbosity count from the `-v` flag.
// 0 = quiet (default), 1 = decisions, 2 = subprocess details, 3 = raw I/O.
var Verbose int

// Debugf writes to stderr when Verbose >= 1.
func Debugf(format string, args ...any) {
	if Verbose >= 1 {
		fmt.Fprintf(os.Stderr, "[browzer] "+format+"\n", args...)
	}
}

// Tracef writes to stderr when Verbose >= 2.
func Tracef(format string, args ...any) {
	if Verbose >= 2 {
		fmt.Fprintf(os.Stderr, "[browzer:trace] "+format+"\n", args...)
	}
}

// Rawf writes to stderr when Verbose >= 3.
func Rawf(format string, args ...any) {
	if Verbose >= 3 {
		fmt.Fprintf(os.Stderr, "[browzer:raw] "+format+"\n", args...)
	}
}

// DumpRaw writes the body to the given writer prefixed with a header,
// when Verbose >= 3. Used by `read` and the daemon client to dump
// pre-filter / post-filter content.
func DumpRaw(w io.Writer, header string, body []byte) {
	if Verbose >= 3 {
		_, _ = fmt.Fprintf(w, "--- %s (%d bytes) ---\n", header, len(body))
		_, _ = w.Write(body)
		_, _ = fmt.Fprintln(w)
	}
}
