package commands

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// emitReadOutput is the shared writer for `workflow get-step` / `get-config` /
// `query` payloads. When --save <path> is set, the payload lands at that path
// and stdout receives a single confirmation line ("wrote N bytes to <abs-path>").
// When --save is absent the payload is written to stdout verbatim.
//
// NOTE: behavior differs from `browzer explore --save` (which is silent + does
// not mkdir parent). The confirmation line + mkdir here are intentional for
// skill-template ergonomics; align in a follow-up if both should converge.
//
// --quiet collapses the confirmation line to nothing — stdout stays empty
// even when --save succeeds. Errors during save (permission denied, read-only
// FS, etc.) still surface on stderr and as a non-zero exit; --quiet does not
// mask those.
//
// The payload may be JSON (from --field / full-step) OR arbitrary human-text
// (from --render); --save persists whatever the verb would have printed.
// os.WriteFile is used directly (not atomic — concurrent reads may observe
// partial state).
//
// payload MUST end with a trailing newline if the caller wants one — this
// helper does NOT append one. Most callers use emitReadJSON (which appends
// a newline) or emitReadRaw (which does not).
func emitReadOutput(cmd *cobra.Command, payload []byte) error {
	savePath, _ := cmd.Flags().GetString("save")
	quiet := auditQuietRequested(cmd)

	if savePath == "" {
		// Default: write to stdout. --quiet does NOT silence read-side stdout
		// because the payload IS the data the caller asked for; suppressing
		// it would break the contract. (Compare to --quiet on writes, where
		// the audit line is pure telemetry.)
		_, err := cmd.OutOrStdout().Write(payload)
		return err
	}

	abs, err := filepath.Abs(savePath)
	if err != nil {
		return fmt.Errorf("--save: resolve %q: %w", savePath, err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("--save: mkdir %q: %w", filepath.Dir(abs), err)
	}
	if err := os.WriteFile(abs, payload, 0o600); err != nil {
		return fmt.Errorf("--save: write %q: %w", abs, err)
	}

	if quiet {
		return nil
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "wrote %s to %s\n", humanizeBytes(len(payload)), abs)
	return err
}

// humanizeBytes formats a byte count for the --save confirmation line.
// Threshold: <4096 bytes → "<n>B" (raw, useful for tiny payloads where the
// exact byte count helps debugging); >=4096 → "<n.n>KiB" (operator-friendly
// for the kilobyte-scale payloads --save was designed for); >=4MiB → MiB.
// Binary units (1024-base) match du / ls -h conventions.
func humanizeBytes(n int) string {
	const (
		kib = 1024
		mib = 1024 * kib
	)
	switch {
	case n < kib*4:
		return fmt.Sprintf("%dB", n)
	case n < mib:
		return fmt.Sprintf("%.1fKiB", float64(n)/float64(kib))
	default:
		return fmt.Sprintf("%.2fMiB", float64(n)/float64(mib))
	}
}

// emitReadJSON is a string-payload convenience wrapper around emitReadOutput
// for JSON payloads (full-step JSON, --field values). Appends a single
// trailing newline if missing to match the historic fmt.Fprintln behaviour.
func emitReadJSON(cmd *cobra.Command, payload string) error {
	if len(payload) > 0 && payload[len(payload)-1] != '\n' {
		payload = payload + "\n"
	}
	return emitReadOutput(cmd, []byte(payload))
}

// emitReadRaw is a string-payload convenience wrapper around emitReadOutput
// for arbitrary text payloads (e.g. --render template output). Does NOT
// append a trailing newline — the rendered text is emitted verbatim.
func emitReadRaw(cmd *cobra.Command, payload string) error {
	return emitReadOutput(cmd, []byte(payload))
}

