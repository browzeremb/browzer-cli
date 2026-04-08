package output

import (
	"encoding/json"
	"fmt"
	"os"
)

// Options controls how Emit routes its payload.
type Options struct {
	// JSON forces compact JSON to stdout (or to Save when set).
	JSON bool
	// Save writes compact JSON to the given file path. Implies JSON.
	Save string
}

// Emit routes a payload to the right destination based on opts:
//
//   - opts.Save set  → write compact JSON to that file (stdout silent)
//   - opts.JSON true → write compact JSON to stdout
//   - otherwise      → write humanText to stdout
//
// Mirrors `emit()` in src/lib/output.ts. Compact JSON (no indent) keeps
// the wire small for SKILLs that pipe through jq/python.
func Emit(payload any, opts Options, humanText string) error {
	if opts.Save != "" {
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal payload: %w", err)
		}
		// Trailing newline so cat-ing the file looks right.
		data = append(data, '\n')
		return os.WriteFile(opts.Save, data, 0o644)
	}
	if opts.JSON {
		enc := json.NewEncoder(os.Stdout)
		// Compact (no SetIndent) — agents prefer single-line JSON.
		return enc.Encode(payload)
	}
	if humanText != "" {
		_, err := fmt.Fprint(os.Stdout, humanText)
		return err
	}
	return nil
}

// Errf writes a formatted message to stderr (no exit). Use for warnings
// like staleness, delete failures, etc. — anything that must NOT pollute
// --json output on stdout.
func Errf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format, args...)
}
