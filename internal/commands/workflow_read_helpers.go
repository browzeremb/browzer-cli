package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// marshalReadJSON marshals v as 2-space-indented JSON with HTML escaping
// disabled. Mirrors the behaviour relied on by `workflow get-step`,
// `describe-step-type`, `schema`, and `validate` payloads — operators
// reading PRD acceptance criteria like `WHEN x > y THEN z` should see the
// literal `>` rather than the HTML-safe `>` escape.
func marshalReadJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// emitReadOutput is the shared writer for workflow read-verb payloads. When
// --save <path> is set, the payload lands at that path and stdout receives a
// single confirmation line ("wrote N bytes to <abs-path>"); --quiet collapses
// that line to nothing. When --save is absent the payload is written to
// stdout verbatim regardless of --quiet (the payload IS the data the caller
// asked for).
func emitReadOutput(cmd *cobra.Command, payload []byte) error {
	savePath, _ := cmd.Flags().GetString("save")
	quiet := auditQuietRequested(cmd)

	if savePath == "" {
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
// Threshold: <4096 bytes -> "<n>B"; >=4096 -> "<n.n>KiB"; >=4MiB -> MiB.
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

