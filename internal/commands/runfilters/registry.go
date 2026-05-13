// Package runfilters maps command patterns to token-compression FilterFns.
// Each filter module registers its function via init() or explicit calls
// against DefaultRegistry.
package runfilters

import (
	"context"
	"math"
	"os"
	"strings"
	"time"

	"github.com/browzeremb/browzer-cli/internal/config"
	"github.com/browzeremb/browzer-cli/internal/daemon"
)

// FilterFn receives the stdout bytes from a successful command and returns
// compressed output.
type FilterFn func(stdout []byte) []byte

// Registry maps a command key to its filter function.
type Registry struct {
	filters map[string]FilterFn
}

// New returns an empty Registry.
func New() *Registry { return &Registry{filters: map[string]FilterFn{}} }

// Register adds a named filter. name should be the canonical verb
// (e.g. "git", "vitest") or a two-word key (e.g. "pnpm turbo", "go test").
func (r *Registry) Register(name string, fn FilterFn) { r.filters[name] = fn }

// Resolve returns the FilterFn for the given argv combination, or nil if no
// filter matches. Matching priority:
//  1. "<verb> <subverb>" (e.g. "pnpm turbo", "go test", "cargo test")
//  2. "<verb>" alone (e.g. "git", "vitest", "biome", "tsc")
func (r *Registry) Resolve(args []string) FilterFn {
	if len(args) == 0 {
		return nil
	}
	if len(args) >= 2 {
		if fn, ok := r.filters[args[0]+" "+args[1]]; ok {
			return fn
		}
	}
	if fn, ok := r.filters[args[0]]; ok {
		return fn
	}
	return nil
}

// CategoryOf returns the canonical category name for a command argv (for
// ledger tagging). Two-word keys are returned with words joined by "-".
func (r *Registry) CategoryOf(args []string) string {
	if len(args) == 0 {
		return "unknown"
	}
	if len(args) >= 2 {
		key := args[0] + " " + args[1]
		if _, ok := r.filters[key]; ok {
			return strings.ReplaceAll(key, " ", "-")
		}
	}
	return args[0]
}

// DefaultRegistry is a Registry pre-populated with all built-in filters.
// Filter modules register via init() calls against this instance.
var DefaultRegistry = New()

// bytesPerToken is the conservative fallback token coefficient used when
// the command output cannot be attributed to a specific language.
// Derived from the cross-language median in the calibration corpus.
const bytesPerToken = 4

// TrackRun emits a token economy Track event for the run proxy.
// Best-effort: errors are silently discarded.
//
// savedTokens is derived from the raw-vs-compressed byte delta divided by
// bytesPerToken. filterLevel labels the category column in the events ledger.
// execMs is the child-process wall-clock duration in milliseconds.
func TrackRun(source string, raw, compressed []byte, filterLevel string, execMs int) error {
	saved := int(math.Round(float64(len(raw)-len(compressed)) / bytesPerToken))
	if saved < 0 {
		saved = 0
	}

	savingsPct := 0.0
	if len(raw) > 0 {
		savingsPct = float64(len(raw)-len(compressed)) / float64(len(raw))
	}

	method := "measured"
	params := daemon.TrackParams{
		TS:               time.Now().UTC().Format(time.RFC3339),
		Source:           source,
		InputBytes:       len(raw),
		OutputBytes:      len(compressed),
		SavedTokens:      saved,
		SavingsPct:       savingsPct,
		ExecMs:           execMs,
		EstimationMethod: &method,
	}
	if filterLevel != "" {
		params.FilterLevel = &filterLevel
	}

	// Use a bounded context timeout so the tracking hop never adds visible
	// latency to the caller. 50 ms is enough for a warm daemon and gives a
	// cold daemon (dial-once DialTimeout is 200 ms) a reasonable window
	// before the best-effort call is abandoned. Any error is discarded.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	cli := daemon.NewClient(config.SocketPath(os.Getuid()))
	_ = cli.Track(ctx, params)
	return nil
}
