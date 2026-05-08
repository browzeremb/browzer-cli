// Package runfilters maps command patterns to token-compression FilterFns.
// Each filter module registers its function via init() or explicit calls
// against DefaultRegistry. TASK_02–04 populate the actual filter logic.
package runfilters

import "strings"

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
// Initially empty — each filter module registers via init() in TASK_02–04.
var DefaultRegistry = New()

// TrackRun emits a token economy Track event for the run proxy.
// Best-effort: errors are silently discarded.
//
// TODO(TASK_07): replace stub with daemon Track wiring. When this starts
// returning non-nil, callers using `_ = runfilters.TrackRun(...)` will
// silently swallow errors — add a test that fails if TrackRun returns non-nil
// so the wiring becomes visible at that time.
func TrackRun(source string, raw, compressed []byte) error {
	// Stub — wired to daemon Track in TASK_07.
	_ = source
	_ = raw
	_ = compressed
	return nil
}
