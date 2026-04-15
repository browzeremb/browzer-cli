// Package commands — spec parsing for non-interactive `workspace docs`.
//
// This file is PURE: no I/O apart from reading an `@file` reference, no
// globals, no cobra dependency. All entry points are unit-testable
// without a TUI, network, or authenticated client. The rest of the
// non-interactive mode (flag wiring, mutual-exclusion checks, delta
// application, safeguards) lives in workspace_docs.go and composes
// these primitives.
//
// Vocabulary:
//
//   - "spec"   — the raw string the user passed after --add/--remove/
//     --replace. Can be a sentinel (new/all/none), an @file reference,
//     a stdlib glob, or a comma-separated list.
//   - "scope"  — which sentinels are legal for the flag being parsed.
//     --add allows `new`; --replace allows `all`/`none`; --remove
//     allows none.
//   - "resolver" — a closure that takes the merged picker items and
//     returns the set of paths matched. Resolution is deferred until
//     after the merge so globs can see the full candidate list.
package commands

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// SpecScope enumerates the sentinels permitted for a given spec site.
// Using a typed enum (vs three booleans) keeps the parser call sites
// self-documenting and prevents "allow `new` on remove" type mistakes.
type SpecScope int

const (
	// SpecScopeAdd permits the `new` sentinel (all not-yet-indexed local
	// files). Used by --add.
	SpecScopeAdd SpecScope = iota
	// SpecScopeRemove permits NO sentinels. Used by --remove.
	SpecScopeRemove
	// SpecScopeReplace permits `all` (every local file) and `none`
	// (empty set). Used by --replace.
	SpecScopeReplace
)

// SpecResolver is the deferred matcher produced by parseSpec. It
// receives the merged item list AT APPLY TIME (so globs can resolve
// against the real candidate set) and returns the set of matching
// relative paths plus any paths the spec referenced but that do not
// exist in the item list.
//
// The `unresolved` slice is what --add uses to hard-error ("path in
// spec but not in candidate set") and what --remove downgrades to a
// stderr warning.
type SpecResolver struct {
	// Resolve returns matched: set of item.RelativePath that matched
	// this spec, and unresolved: literal paths the user typed that
	// could not be located in items (empty for sentinel/glob specs).
	Resolve func(items []DocPickerItem) (matched map[string]bool, unresolved []string)
	// Raw is the original spec string, retained for error messages.
	Raw string
	// Sentinel, when non-empty, names the sentinel form this spec used
	// (`new`, `all`, `none`). Useful for downstream logic that needs to
	// distinguish "explicit empty" (`none`) from "empty match".
	Sentinel string
}

// parseSpec turns a raw spec string into a SpecResolver, validating
// against the allowed sentinels for `scope`. Parse order matches the
// documented precedence:
//
//  1. Sentinel (exact string match, scope-gated)
//  2. `@file` reference (read file, one path per line)
//  3. Glob (contains *, ?, or [)
//  4. Comma-separated literal list
//
// The `**` recursive-glob is explicitly rejected — we intentionally use
// the stdlib `path.Match` (forward-slash aware, single path element per
// star) and do not take a doublestar dep.
func parseSpec(s string, scope SpecScope) (*SpecResolver, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty spec")
	}

	// (1) Sentinel — only for the exact string, scope-dependent.
	if sent := matchSentinel(s, scope); sent != "" {
		return newSentinelResolver(s, sent), nil
	}
	// Reject sentinels being used in the wrong scope BEFORE falling
	// through to "treat as literal path". Otherwise a user who typed
	// `--remove new` would silently mean "remove a file literally
	// named 'new'", which is almost certainly not what they meant.
	if isAnySentinel(s) {
		var valid []string
		switch scope {
		case SpecScopeAdd:
			valid = []string{"new"}
		case SpecScopeReplace:
			valid = []string{"all", "none"}
		case SpecScopeRemove:
			// No sentinels accepted for --remove.
		}
		if len(valid) == 0 {
			return nil, fmt.Errorf("sentinel %q is not allowed for this flag (no sentinels accepted — use a literal path, glob, or @file)", s)
		}
		return nil, fmt.Errorf("sentinel %q is not allowed for this flag (accepted: %s)", s, strings.Join(valid, ", "))
	}

	// (2) @file reference.
	if strings.HasPrefix(s, "@") {
		paths, err := readSpecFile(strings.TrimPrefix(s, "@"))
		if err != nil {
			return nil, err
		}
		return newLiteralResolver(s, paths), nil
	}

	// (3) Glob — any of * ? [.
	if strings.ContainsAny(s, "*?[") {
		if strings.Contains(s, "**") {
			return nil, fmt.Errorf("'**' is not supported; use stdlib glob patterns like 'docs/*.md' or a comma list")
		}
		// Validate the pattern eagerly so the user gets a clear error
		// now instead of at apply time. Use stdlib `path.Match` (forward
		// slash only) NOT `filepath.Match` — the latter respects the
		// host OS separator, which on POSIX would match `/` but on
		// Windows would silently treat it as a literal character and
		// make `docs/*.md` never match. RelativePath on picker items
		// is always normalized to forward slashes (walker guarantee),
		// so path.Match is the correct choice on every platform.
		if _, err := path.Match(s, "probe"); err != nil {
			return nil, fmt.Errorf("invalid glob pattern %q: %w", s, err)
		}
		return newGlobResolver(s), nil
	}

	// (4) Comma list — fall-through.
	parts := strings.Split(s, ",")
	paths := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("spec resolved to empty path list: %q", s)
	}
	return newLiteralResolver(s, paths), nil
}

// matchSentinel returns the sentinel name when `s` is a valid sentinel
// for the given scope, or empty string otherwise.
func matchSentinel(s string, scope SpecScope) string {
	switch scope {
	case SpecScopeAdd:
		if s == "new" {
			return "new"
		}
	case SpecScopeReplace:
		if s == "all" || s == "none" {
			return s
		}
	case SpecScopeRemove:
		// No sentinels allowed.
	}
	return ""
}

// isAnySentinel reports whether `s` is ANY known sentinel (independent
// of scope). Used to differentiate "wrong scope" from "literal path".
func isAnySentinel(s string) bool {
	return s == "new" || s == "all" || s == "none"
}

// newSentinelResolver builds a resolver whose Resolve function
// interprets the sentinel against the merged items at apply time.
func newSentinelResolver(raw, sentinel string) *SpecResolver {
	return &SpecResolver{
		Raw:      raw,
		Sentinel: sentinel,
		Resolve: func(items []DocPickerItem) (map[string]bool, []string) {
			matched := make(map[string]bool)
			switch sentinel {
			case "new":
				// All items that have a local file AND are not yet
				// indexed — exactly the rows a user means by "new".
				for _, it := range items {
					if it.HasLocal() && !it.Indexed {
						matched[it.RelativePath] = true
					}
				}
			case "all":
				// Every item with a local file. A server-only (ghost)
				// row has no bytes to upload so it can't be "kept"
				// under --replace all; it just becomes a delete.
				for _, it := range items {
					if it.HasLocal() {
						matched[it.RelativePath] = true
					}
				}
			case "none":
				// Empty set — intentional no-match.
			}
			return matched, nil
		},
	}
}

// newLiteralResolver builds a resolver that exact-matches a set of
// literal relative paths against items. Paths referenced but not found
// are returned as unresolved.
func newLiteralResolver(raw string, paths []string) *SpecResolver {
	// Normalize to forward-slash for comparison against
	// item.RelativePath (which the walker always emits that way).
	norm := make([]string, len(paths))
	for i, p := range paths {
		norm[i] = filepath.ToSlash(p)
	}
	return &SpecResolver{
		Raw: raw,
		Resolve: func(items []DocPickerItem) (map[string]bool, []string) {
			index := make(map[string]struct{}, len(items))
			for _, it := range items {
				index[it.RelativePath] = struct{}{}
			}
			matched := make(map[string]bool)
			var unresolved []string
			for _, p := range norm {
				if _, ok := index[p]; ok {
					matched[p] = true
				} else {
					unresolved = append(unresolved, p)
				}
			}
			return matched, unresolved
		},
	}
}

// newGlobResolver builds a resolver that uses stdlib `path.Match`
// (forward-slash only) against every item's relative path. See the
// parseSpec validation site for the reason this must NOT use
// `filepath.Match`. A glob with no matches is NOT an error — agents
// often chain several globs and expect empty ones to be no-ops.
func newGlobResolver(pattern string) *SpecResolver {
	return &SpecResolver{
		Raw: pattern,
		Resolve: func(items []DocPickerItem) (map[string]bool, []string) {
			matched := make(map[string]bool)
			for _, it := range items {
				// path.Match errors only on malformed patterns;
				// we already validated in parseSpec, so ignore here.
				ok, _ := path.Match(pattern, it.RelativePath)
				if ok {
					matched[it.RelativePath] = true
				}
			}
			return matched, nil
		},
	}
}

// readSpecFile reads a list of relative paths from a file referenced
// via `@path`. Lines beginning with `#` and blank lines are skipped so
// agents can annotate the file. Errors are wrapped with the path for
// easy debugging.
func readSpecFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open spec file %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read spec file %q: %w", path, err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("spec file %q contained no paths", path)
	}
	return out, nil
}

// SpecMode identifies which flag produced a resolver — used by
// applySpecsToItems to pick the right base state.
type SpecMode int

const (
	// SpecModeAdd: base = current indexed state, additive selection.
	SpecModeAdd SpecMode = iota
	// SpecModeRemove: base = current indexed state, subtractive.
	SpecModeRemove
	// SpecModeReplace: base = empty, resolver defines final state.
	SpecModeReplace
)

// applySpecsToItems mutates items[i].Selected according to the supplied
// specs. It is deliberately split out from the cobra RunE so that tests
// can assert the selection logic without any flag parsing or I/O.
//
// Rules (see top-of-file spec):
//
//   - In REPLACE mode, base selection is cleared; replaceSpec defines
//     the final selected set. addSpec/removeSpec must be nil.
//   - In ADD/REMOVE mode, base selection = Indexed (current server
//     state). addSpec unions items in; removeSpec removes them.
//
// Returns the combined list of unresolved paths, partitioned by which
// spec they came from so the caller can error (add) or warn (remove).
type ApplyResult struct {
	// UnresolvedAdd are paths in --add's spec that had no matching item
	// — caller MUST error.
	UnresolvedAdd []string
	// UnresolvedRemove are paths in --remove's spec that had no
	// matching indexed item — caller SHOULD warn and continue.
	UnresolvedRemove []string
	// UnresolvedReplace are paths in --replace's spec that had no
	// matching item — caller MUST error.
	UnresolvedReplace []string
}

// applySpecsToItems applies the non-interactive selection semantics.
// Exactly one of (addSpec|removeSpec) or replaceSpec may be set; the
// caller enforces mutual exclusion at flag-parse time.
func applySpecsToItems(items []DocPickerItem, addSpec, removeSpec, replaceSpec *SpecResolver) (ApplyResult, []DocPickerItem) {
	out := make([]DocPickerItem, len(items))
	copy(out, items)
	var res ApplyResult

	if replaceSpec != nil {
		// Base = nothing selected. The spec defines the final set.
		matched, unresolved := replaceSpec.Resolve(out)
		res.UnresolvedReplace = unresolved
		for i := range out {
			out[i].Selected = matched[out[i].RelativePath]
		}
		return res, out
	}

	// ADD/REMOVE mode. Base = "everything currently indexed" — same as
	// the default merge produces, but we recompute explicitly here so
	// that any future change to the merge default cannot silently
	// break this path.
	for i := range out {
		out[i].Selected = out[i].Indexed
	}

	if addSpec != nil {
		matched, unresolved := addSpec.Resolve(out)
		res.UnresolvedAdd = unresolved
		for i := range out {
			if matched[out[i].RelativePath] {
				out[i].Selected = true
			}
		}
	}
	if removeSpec != nil {
		matched, unresolved := removeSpec.Resolve(out)
		// For --remove we filter unresolved down to "was not indexed"
		// — if the path IS in the item list but simply isn't indexed,
		// that's a no-op, not a warning.
		res.UnresolvedRemove = append(res.UnresolvedRemove, unresolved...)
		// Also warn for paths that matched but aren't currently
		// indexed (user said "remove X" but X was never there).
		for i := range out {
			if matched[out[i].RelativePath] {
				if !out[i].Indexed {
					res.UnresolvedRemove = append(res.UnresolvedRemove, out[i].RelativePath)
					continue
				}
				out[i].Selected = false
			}
		}
	}
	return res, out
}
