package runfilters

import (
	"bytes"
	"regexp"
)

// Package-level compiled regexes — compiled once at init, not on every call.
var (
	pnpmSummaryRe = regexp.MustCompile(`Packages:\s*((?:[+-~]\d+\s*)+)`)
	pnpmElapsedRe = regexp.MustCompile(`Done in ([\d.]+(?:ms|s))`)
	pnpmAddedRe   = regexp.MustCompile(`(?i)added\s+(\d+)\s+packages?`)
	pnpmRemovedRe = regexp.MustCompile(`(?i)removed\s+(\d+)\s+packages?`)
	pnpmChangedRe = regexp.MustCompile(`(?i)changed\s+(\d+)\s+packages?`)
	pnpmAddRe2    = regexp.MustCompile(`\+(\d+)`)
	pnpmDelRe2    = regexp.MustCompile(`-(\d+)`)
	pnpmChgRe2    = regexp.MustCompile(`~(\d+)`)
)

func init() {
	DefaultRegistry.Register("pnpm install", filterPnpmInstall)
	DefaultRegistry.Register("npm install", filterPnpmInstall)
}

// filterPnpmInstall compresses pnpm/npm install output to a single summary line.
// Typical pnpm install output is 10–40 KB; the filter targets >85 % byte reduction.
//
// Recognised patterns (any combination):
//   - "Packages: +N added"           or "added N packages"
//   - "Packages: -N removed"         or "removed N packages"
//   - "Packages: ~N updated/changed" or "changed N packages"
//   - "Done in Xs" / "Done in Xms"   elapsed time
//
// Fallback contract: when none of the above patterns match (e.g. network error
// output, lockfile conflicts, or future pnpm output format changes), the raw
// stdout is returned unchanged so no signal is lost. TrackRun is still called
// by the run command with source "browzer-run-pnpm-install" and savings=0,
// making zero-compression runs visible in the token-economy ledger.
func filterPnpmInstall(stdout []byte) []byte {
	var added, removed, changed, elapsed string

	// Try pnpm v8+ consolidated summary line first.
	if m := pnpmSummaryRe.FindSubmatch(stdout); m != nil {
		summary := string(m[1])
		if a := pnpmAddRe2.FindStringSubmatch(summary); a != nil {
			added = a[1]
		}
		if d := pnpmDelRe2.FindStringSubmatch(summary); d != nil {
			removed = d[1]
		}
		if c := pnpmChgRe2.FindStringSubmatch(summary); c != nil {
			changed = c[1]
		}
	} else {
		// npm / pnpm v7 per-line patterns.
		if m := pnpmAddedRe.FindSubmatch(stdout); m != nil {
			added = string(m[1])
		}
		if m := pnpmRemovedRe.FindSubmatch(stdout); m != nil {
			removed = string(m[1])
		}
		if m := pnpmChangedRe.FindSubmatch(stdout); m != nil {
			changed = string(m[1])
		}
	}

	if m := pnpmElapsedRe.FindSubmatch(stdout); m != nil {
		elapsed = string(m[1])
	}

	// Fallback: no patterns matched — return raw to preserve signal.
	if added == "" && removed == "" && changed == "" {
		return stdout
	}

	var parts [][]byte
	if added != "" {
		parts = append(parts, []byte("added "+added+" packages"))
	}
	if removed != "" {
		parts = append(parts, []byte("removed "+removed))
	}
	if changed != "" {
		parts = append(parts, []byte("changed "+changed))
	}

	line := bytes.Join(parts, []byte(", "))
	if elapsed != "" {
		line = append(line, []byte(" ("+elapsed+")")...)
	}
	line = append(line, '\n')
	return line
}
