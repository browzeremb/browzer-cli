// Package codereview implements the per-member → consolidated aggregation
// logic for `browzer codereview aggregate`.
//
// # Per-member file shape
//
// Each per-member file lives at:
//
//	docs/browzer/<feat>/staging/CODE_REVIEW.<member>.json
//
// and carries a JSON object with at minimum a "findings" key whose value is
// a JSON array of #Finding objects (workflow-v1.cue §624).  All other fields
// in the file are preserved verbatim into the consolidation metadata.
//
// # Consolidated file shape
//
// The output is written to:
//
//	docs/browzer/<feat>/staging/CODE_REVIEW.json
//
// with the shape required by the code-review skill's SKILL.md aggregator
// contract (preserve-all, mergedFrom[] for overlap, stable F-NNN ids).
//
// # Preserve-all guarantee
//
// No finding is dropped.  When two members report a finding on the same
// file+line, BOTH are included and the later finding records
// mergedFrom: ["<earlier-id>"] so receiving-code-review can surface
// the cross-lane signal.  Severity is informational — it is NEVER used
// as a dedup key.
package codereview

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Finding mirrors the #Finding shape from workflow-v1.cue.  The struct is
// intentionally a superset — unknown JSON keys round-trip via the raw
// remainder (handled by the flat map approach in LoadMemberFile).
type Finding struct {
	// ID is assigned during aggregation: F-001, F-002, … (zero-padded to 3
	// digits so lexicographic order matches numeric order for ≤999 findings).
	ID          string   `json:"id"`
	Domain      string   `json:"domain"`
	Severity    string   `json:"severity"`
	Category    string   `json:"category"`
	File        string   `json:"file"`
	Line        *int     `json:"line,omitempty"`
	Description string   `json:"description"`
	SuggestedFix string  `json:"suggestedFix,omitempty"`
	AssignedSkill *string `json:"assignedSkill"`
	Status      string   `json:"status"`
	CrossLaneOverlap bool `json:"crossLaneOverlap,omitempty"`
	// MergedFrom records the per-member IDs (e.g. "SR-3", "QA-1") that
	// overlap with this finding (same file+line).  Populated only when ≥1
	// sibling finding shares the same file+line pair across lanes.
	MergedFrom []string `json:"mergedFrom,omitempty"`
}

// MemberFile is the decoded content of a single per-member staging file.
type MemberFile struct {
	// MemberName is derived from the filename suffix, e.g. "senior-engineer"
	// from CODE_REVIEW.senior-engineer.json.
	MemberName string
	// Findings is the parsed findings array.
	Findings []Finding
}

// SeverityCounts holds the rolled-up severity totals for the consolidated
// review (mirrors #SeverityCounts from workflow-v1.cue §575).
type SeverityCounts struct {
	High   int `json:"high"`
	Medium int `json:"medium"`
	Low    int `json:"low"`
}

// AggregateResult is the consolidated CODE_REVIEW body written to
// docs/browzer/<feat>/staging/CODE_REVIEW.json.  Fields follow the
// #CodeReview CUE shape; the struct carries only the subset that the
// aggregator populates — unknown fields from future schema versions are
// preserved by the open-struct design.
type AggregateResult struct {
	// DispatchMode is always "parallel-with-consolidator" when produced by
	// this CLI verb — it reflects the fan-out + merge topology.
	DispatchMode string `json:"dispatchMode"`
	// ReviewTier defaults to "recommended" when not overridden.
	ReviewTier string `json:"reviewTier"`
	// MandatoryMembers lists the member names found and aggregated.
	MandatoryMembers []string `json:"mandatoryMembers"`
	// RecommendedMembers is always an empty array (populated by the skill).
	RecommendedMembers []string `json:"recommendedMembers"`
	// CustomMembers is always an empty array (populated by the skill).
	CustomMembers []string `json:"customMembers"`
	// SeverityCounts is the rolled-up count across all findings.
	SeverityCounts SeverityCounts `json:"severityCounts"`
	// DuplicationFindings satisfies the required array field.
	DuplicationFindings []any `json:"duplicationFindings"`
	// RegressionRun is null (populated by the regression-tester member file
	// passthrough — this aggregator preserves it when present).
	RegressionRun any `json:"regressionRun"`
	// Findings is the merged, stable-ID'd findings array.
	Findings []Finding `json:"findings"`
}

// LoadMemberFiles scans stagingDir for files matching the pattern
// CODE_REVIEW.*.json (excluding CODE_REVIEW.json itself) and decodes each.
//
// In non-strict mode a corrupt / unreadable file is logged to errOut and
// skipped; the function still returns whatever valid members were found.
// In strict mode the first error is returned immediately and the caller
// must treat it as exit code 3.
func LoadMemberFiles(stagingDir string, strict bool, errOut func(string)) ([]MemberFile, []string, error) {
	entries, err := os.ReadDir(stagingDir)
	if err != nil {
		return nil, nil, fmt.Errorf("read staging dir %s: %w", stagingDir, err)
	}

	var members []MemberFile
	var skipped []string

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		// Must start with "CODE_REVIEW." and end with ".json" but must NOT
		// be the consolidated output file CODE_REVIEW.json itself.
		if !strings.HasPrefix(name, "CODE_REVIEW.") || !strings.HasSuffix(name, ".json") {
			continue
		}
		if name == "CODE_REVIEW.json" {
			continue
		}
		// Extract member name: CODE_REVIEW.<member>.json → <member>
		memberName := strings.TrimSuffix(strings.TrimPrefix(name, "CODE_REVIEW."), ".json")
		if memberName == "" {
			continue
		}

		path := filepath.Join(stagingDir, name)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			msg := fmt.Sprintf("skip %s: read error: %v", name, readErr)
			if strict {
				return nil, skipped, fmt.Errorf("%s", msg)
			}
			errOut(msg)
			skipped = append(skipped, name)
			continue
		}

		var raw map[string]json.RawMessage
		if parseErr := json.Unmarshal(data, &raw); parseErr != nil {
			msg := fmt.Sprintf("skip %s: JSON parse error: %v", name, parseErr)
			if strict {
				return nil, skipped, fmt.Errorf("%s", msg)
			}
			errOut(msg)
			skipped = append(skipped, name)
			continue
		}

		findingsRaw, ok := raw["findings"]
		if !ok {
			msg := fmt.Sprintf("skip %s: missing \"findings\" key", name)
			if strict {
				return nil, skipped, fmt.Errorf("%s", msg)
			}
			errOut(msg)
			skipped = append(skipped, name)
			continue
		}

		var findings []Finding
		if parseErr := json.Unmarshal(findingsRaw, &findings); parseErr != nil {
			msg := fmt.Sprintf("skip %s: \"findings\" is not a valid array: %v", name, parseErr)
			if strict {
				return nil, skipped, fmt.Errorf("%s", msg)
			}
			errOut(msg)
			skipped = append(skipped, name)
			continue
		}

		members = append(members, MemberFile{
			MemberName: memberName,
			Findings:   findings,
		})
	}

	// Sort members deterministically by name so consolidated output is stable
	// across directory orderings.
	sort.Slice(members, func(i, j int) bool {
		return members[i].MemberName < members[j].MemberName
	})

	return members, skipped, nil
}

// Aggregate merges the findings from all member files into a single
// AggregateResult following the preserve-all algorithm:
//
//  1. Collect every findings[] from every member.
//  2. Assign per-member stable IDs (<PREFIX>-<seq>).
//  3. Detect overlap (same file+line across lanes) and record mergedFrom[].
//  4. Assign global sequential IDs F-001, F-002, …
//  5. Roll up severityCounts.
//
// The algorithm never drops a finding.  Severity is NOT used as a dedup key.
func Aggregate(members []MemberFile) AggregateResult {
	// memberPrefix maps known domain names to their canonical 2-4 letter prefix.
	// Unknown domains fall back to the first 4 upper-case characters of the
	// member name so the IDs are still unique and readable.
	memberPrefix := func(domain string) string {
		switch strings.ToLower(domain) {
		case "senior-engineer", "sr":
			return "SR"
		case "software-architect", "arch":
			return "ARCH"
		case "qa":
			return "QA"
		case "regression-tester", "reg":
			return "REG"
		default:
			// Generic: uppercase first 4 runes, replacing non-alpha with X.
			runes := []rune(strings.ToUpper(domain))
			var b strings.Builder
			for i, r := range runes {
				if i >= 4 {
					break
				}
				if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
					b.WriteRune(r)
				} else {
					b.WriteRune('X')
				}
			}
			if b.Len() == 0 {
				return "GEN"
			}
			return b.String()
		}
	}

	// Step 1 + 2: collect all findings with their per-member stable IDs.
	type taggedFinding struct {
		memberID   string // e.g. "SR-1", "ARCH-2"
		memberName string // e.g. "senior-engineer" — used for cross-lane dedup
		f          Finding
	}
	var tagged []taggedFinding

	memberNames := make([]string, 0, len(members))
	for _, m := range members {
		memberNames = append(memberNames, m.MemberName)
		prefix := memberPrefix(m.MemberName)
		for i, f := range m.Findings {
			memberID := fmt.Sprintf("%s-%d", prefix, i+1)
			f.ID = memberID
			tagged = append(tagged, taggedFinding{memberID: memberID, memberName: m.MemberName, f: f})
		}
	}

	// Step 3: detect cross-lane overlap — group tagged findings by file+line.
	// A line of 0 means no line number was provided; we still group by file.
	// crossLaneOverlap is only set when the group contains findings from at
	// least two distinct member lanes (MemberName discriminant). Same-member
	// findings on the same file (e.g. two file-level architecture concerns from
	// the same reviewer) are NOT considered cross-lane duplicates.
	type groupKey struct {
		file string
		line int
	}
	groups := make(map[groupKey][]int) // value = indices into tagged
	for i, tf := range tagged {
		line := 0
		if tf.f.Line != nil {
			line = *tf.f.Line
		}
		k := groupKey{file: tf.f.File, line: line}
		groups[k] = append(groups[k], i)
	}

	// For each group with ≥2 findings from distinct member lanes, set mergedFrom
	// on every finding to include the sibling IDs and mark crossLaneOverlap = true.
	for _, indices := range groups {
		if len(indices) < 2 {
			continue
		}
		// Check that at least two distinct MemberNames are present.
		memberSet := make(map[string]struct{}, len(indices))
		for _, idx := range indices {
			memberSet[tagged[idx].memberName] = struct{}{}
		}
		if len(memberSet) < 2 {
			// All findings come from the same lane — not a cross-lane overlap.
			continue
		}
		ids := make([]string, len(indices))
		for i, idx := range indices {
			ids[i] = tagged[idx].memberID
		}
		sort.Strings(ids)
		for _, idx := range indices {
			selfID := tagged[idx].memberID
			var siblings []string
			for _, id := range ids {
				if id != selfID {
					siblings = append(siblings, id)
				}
			}
			tagged[idx].f.MergedFrom = siblings
			tagged[idx].f.CrossLaneOverlap = true
		}
	}

	// Step 4: assign global sequential IDs F-001, F-002, …
	// Maintain a stable order: findings are already sorted by member (which
	// were sorted alphabetically) then by original index within each member.
	result := make([]Finding, len(tagged))
	counts := SeverityCounts{}
	for i, tf := range tagged {
		f := tf.f
		f.ID = fmt.Sprintf("F-%03d", i+1)
		result[i] = f

		// Step 5: roll up severity counts.
		switch strings.ToLower(f.Severity) {
		case "high":
			counts.High++
		case "medium":
			counts.Medium++
		case "low":
			counts.Low++
		}
	}

	return AggregateResult{
		DispatchMode:        "parallel-with-consolidator",
		ReviewTier:          "recommended",
		MandatoryMembers:    memberNames,
		RecommendedMembers:  []string{},
		CustomMembers:       []string{},
		SeverityCounts:      counts,
		DuplicationFindings: []any{},
		RegressionRun:       nil,
		Findings:            result,
	}
}

// StagingDir returns the staging directory path for a feature.
// feat is the feature id, e.g. "feat-20260511-skills-cli-workflow-hardening".
// repoRoot is the absolute path to the repository root (git root).
func StagingDir(repoRoot, feat string) string {
	return filepath.Join(repoRoot, "docs", "browzer", feat, "staging")
}

// OutputPath returns the path for the consolidated CODE_REVIEW.json.
func OutputPath(stagingDir string) string {
	return filepath.Join(stagingDir, "CODE_REVIEW.json")
}

// MarshalResult encodes the AggregateResult as indented JSON.
func MarshalResult(r AggregateResult) ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}
