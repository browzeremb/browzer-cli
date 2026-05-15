package hooks

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
	"github.com/spf13/cobra"
)

// promptHookInput models the UserPromptSubmit hook envelope. The guard
// reads the prompt body plus a couple of side-channel signals: the
// session id (for per-session dedup) and is_assistant_turn (which
// suppresses the guard on model self-talk).
type promptHookInput struct {
	Prompt          string `json:"prompt"`
	UserPrompt      string `json:"user_prompt"`
	Message         string `json:"message"`
	SessionID       string `json:"session_id"`
	SessionIDCamel  string `json:"sessionId"`
	IsAssistantTurn bool   `json:"is_assistant_turn"`
	CWD             string `json:"cwd"`
}

// promptHookSpecificOutput is the inner payload of the UserPromptSubmit
// guard response. Unlike the PreToolUse rewrite envelope this carries
// only hookEventName + additionalContext — no permissionDecision, no
// updatedInput.
type promptHookSpecificOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// promptEnvelope is the outer JSON object emitted on stdout when the
// guard decides to surface an advisory.
type promptEnvelope struct {
	HookSpecificOutput *promptHookSpecificOutput `json:"hookSpecificOutput"`
}

// excludeFile mirrors the optional .browzer/search-triggers.exclude.json
// shape. All fields are optional.
type excludeFile struct {
	Keywords []string `json:"exclude_keywords"`
	Patterns []string `json:"exclude_patterns"`
}

// clock indirection lets tests stamp deterministic dedup timestamps.
var nowMs = func() int64 { return time.Now().UnixMilli() }

// tmpDirFn lets tests override the dedup-state directory location.
var tmpDirFn = os.TempDir

// WithNowMs swaps the package-level clock seam and returns a restore
// closure that reverts it. Test-only — never call from production code.
func WithNowMs(fn func() int64) (restore func()) {
	prev := nowMs
	nowMs = fn
	return func() { nowMs = prev }
}

// WithTmpDir swaps the package-level temp-dir seam and returns a restore
// closure that reverts it. Test-only — never call from production code.
func WithTmpDir(fn func() string) (restore func()) {
	prev := tmpDirFn
	tmpDirFn = fn
	return func() { tmpDirFn = prev }
}

// newPromptGuardCommand returns the real `browzer hook prompt-guard`
// cobra command, replacing the generic stub registered in hooks.go.
func newPromptGuardCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "prompt-guard",
		Short: "Inspect and optionally block user prompts",
		RunE:  runPromptGuard,
	}
}

// runPromptGuard is the cobra RunE for the prompt-guard hook.
// Exit-code protocol:
//
//   - exit 0 (ExitAllow)       — advisory emitted; Claude Code proceeds.
//   - exit 1 (ExitPassthrough) — no decision; Claude Code keeps default.
func runPromptGuard(cmd *cobra.Command, _ []string) error {
	raw, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		fmt.Fprintf(os.Stderr, "browzer hook prompt-guard: stdin read failed: %v\n", err)
		os.Exit(internalhooks.ExitPassthrough)
	}
	code, envelope := promptGuardDecide(raw)
	if envelope != nil {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetEscapeHTML(false)
		_ = enc.Encode(envelope)
	}
	os.Exit(code)
	return nil // unreachable
}

// promptGuardDecide is the testable core of the guard. It returns the
// exit code and, on the advisory path, a non-nil envelope to write to
// stdout. The caller is responsible for emitting then os.Exit(code).
func promptGuardDecide(raw []byte) (int, *promptEnvelope) {
	if !internalhooks.GateAllowed("prompt-guard") {
		return internalhooks.ExitPassthrough, nil
	}
	if !internalhooks.IsInBrowzerWorkspace("") {
		return internalhooks.ExitPassthrough, nil
	}

	var in promptHookInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return internalhooks.ExitPassthrough, nil
	}

	// Assistant-turn guard: never fire on model self-talk or tool-result
	// re-injection. Returning before any vocab work is the strongest
	// invariant of this hook — every other gate sits below.
	if in.IsAssistantTurn {
		return internalhooks.ExitPassthrough, nil
	}

	prompt := firstNonEmpty(in.Prompt, in.UserPrompt, in.Message)
	if len(prompt) < MinPromptLen {
		return internalhooks.ExitPassthrough, nil
	}
	if SlashCommandRE.MatchString(prompt) {
		return internalhooks.ExitPassthrough, nil
	}

	// Plan-mode redirect short-circuits everything else — emit the
	// generate-prd / generate-task advisory and exit.
	for _, re := range PlanTriggers() {
		if re.MatchString(prompt) {
			return internalhooks.ExitAllow, &promptEnvelope{
				HookSpecificOutput: &promptHookSpecificOutput{
					HookEventName:     "UserPromptSubmit",
					AdditionalContext: PlanModeAdvisory,
				},
			}
		}
	}

	cwd := in.CWD
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			cwd = "."
		}
	}

	// Load extensible vocab from .browzer/search-triggers.json (optional).
	baseVocab := DefaultVocab()
	vocab := make([]string, 0, len(baseVocab)+8)
	vocab = append(vocab, baseVocab...)
	if extra, ok := readStringArrayFile(filepath.Join(cwd, ".browzer", "search-triggers.json")); ok {
		vocab = append(vocab, extra...)
	}

	// Exclude file is optional; keyword + regex matches → passthrough.
	// is_assistant_turn was already checked above and returns early, so only
	// the shared Keywords and Patterns fields are evaluated here.
	if excl, ok := readExcludeFile(filepath.Join(cwd, ".browzer", "search-triggers.exclude.json")); ok {
		lower := strings.ToLower(prompt)
		for _, k := range excl.Keywords {
			if strings.Contains(lower, strings.ToLower(k)) {
				return internalhooks.ExitPassthrough, nil
			}
		}
		for _, src := range excl.Patterns {
			re, err := regexp.Compile(`(?i)` + src)
			if err != nil {
				continue
			}
			if re.MatchString(prompt) {
				return internalhooks.ExitPassthrough, nil
			}
		}
	}

	hits := collectHits(prompt, vocab)
	if len(hits) == 0 {
		return internalhooks.ExitPassthrough, nil
	}
	if !ActionVerbRE.MatchString(prompt) {
		return internalhooks.ExitPassthrough, nil
	}
	if allScoped(hits) {
		return internalhooks.ExitPassthrough, nil
	}

	// Per-session dedup: same session + same sorted hit-set → skip.
	sessionID := firstNonEmpty(in.SessionID, in.SessionIDCamel)
	fingerprint := hitsFingerprint(hits)
	if dedupSessionReminder(sessionID, fingerprint) {
		return internalhooks.ExitPassthrough, nil
	}

	return internalhooks.ExitAllow, &promptEnvelope{
		HookSpecificOutput: &promptHookSpecificOutput{
			HookEventName:     "UserPromptSubmit",
			AdditionalContext: buildAdvisoryText(hits),
		},
	}
}

// collectHits applies vocab, co-occurrence, scoped-package, install, and
// import detection to the prompt and returns a deduplicated, stably-
// ordered slice of hit strings.
func collectHits(prompt string, vocab []string) []string {
	seen := make(map[string]struct{}, 16)
	add := func(s string) {
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
	}

	lower := strings.ToLower(prompt)

	// Vocab match: word-boundary for single tokens, substring for
	// multi-token entries (which already carry whitespace or `.` or `/`).
	for _, term := range vocab {
		t := strings.ToLower(term)
		if strings.ContainsAny(t, " /.") {
			if strings.Contains(lower, t) {
				add(term)
			}
			continue
		}
		re, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(t) + `\b`)
		if err != nil {
			continue
		}
		if re.MatchString(prompt) {
			add(term)
		}
	}

	// Co-occurrence vocab: term only counts when its required cue also matches.
	for _, rule := range CooccurrenceVocab() {
		re, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(rule.Term) + `\b`)
		if err != nil {
			continue
		}
		if re.MatchString(prompt) && rule.RequiresRE.MatchString(prompt) {
			add(rule.Term)
		}
	}

	// Scoped npm packages.
	for _, m := range ScopedPackageRE.FindAllString(prompt, -1) {
		add(m)
	}
	// Install command arguments.
	for _, m := range InstallCommandRE.FindAllStringSubmatch(prompt, -1) {
		if len(m) >= 2 {
			add(m[1])
		}
	}
	// Import / require sources — skip relative paths.
	for _, m := range ImportSourceRE.FindAllStringSubmatch(prompt, -1) {
		src := m[1]
		if src == "" && len(m) >= 3 {
			src = m[2]
		}
		if src == "" {
			continue
		}
		if strings.HasPrefix(src, ".") || strings.HasPrefix(src, "/") {
			continue
		}
		add(src)
	}

	// Return in stable insertion-ish order: collect keys then sort for
	// reproducible fingerprints / preview strings across runs.
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// allScoped reports whether every hit begins with "@" — vector search
// seeds derived from scoped-only hits perform poorly, so we skip.
func allScoped(hits []string) bool {
	if len(hits) == 0 {
		return false
	}
	for _, h := range hits {
		if !strings.HasPrefix(h, "@") {
			return false
		}
	}
	return true
}

// hitsFingerprint returns the first 16 hex chars of SHA-1 over the
// sorted hit list joined by "|". Stable across runs given the same set.
func hitsFingerprint(hits []string) string {
	sorted := make([]string, len(hits))
	copy(sorted, hits)
	sort.Strings(sorted)
	sum := sha1.Sum([]byte(strings.Join(sorted, "|")))
	return hex.EncodeToString(sum[:])[:16]
}

// dedupSessionReminder records the fingerprint under
// $TMPDIR/.browzer-guard/<sessionID>.json and returns true when the same
// fingerprint was already seen for this session. Best-effort: any I/O
// failure returns false so a real prompt is never silently swallowed.
func dedupSessionReminder(sessionID, fingerprint string) bool {
	if sessionID == "" {
		return false
	}
	dir := filepath.Join(tmpDirFn(), ".browzer-guard")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false
	}
	file := filepath.Join(dir, sessionID+".json")
	seen := map[string]int64{}
	if raw, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(raw, &seen)
	}
	if _, ok := seen[fingerprint]; ok {
		return true
	}
	seen[fingerprint] = nowMs()
	if out, err := json.Marshal(seen); err == nil {
		_ = os.WriteFile(file, out, 0o600)
	}
	return false
}

// buildAdvisoryText renders the [Browzer search guard] additionalContext
// body. Hits are previewed (up to 6), @-scoped paths are stripped from
// the search seed when non-scoped alternatives exist, and a NOTE is
// appended when stripping happened.
func buildAdvisoryText(hits []string) string {
	preview := make([]string, 0, 6)
	for i, h := range hits {
		if i >= 6 {
			break
		}
		preview = append(preview, `"`+h+`"`)
	}

	nonScoped := make([]string, 0, len(hits))
	for _, h := range hits {
		if !strings.HasPrefix(h, "@") {
			nonScoped = append(nonScoped, h)
		}
	}
	var seedParts []string
	if len(nonScoped) > 0 {
		seedParts = nonScoped
	} else {
		// Strip @scope/ prefix so the model has at least the bare name.
		stripScopeRE := regexp.MustCompile(`^@[^/]+\/`)
		seedParts = make([]string, 0, len(hits))
		for _, h := range hits {
			seedParts = append(seedParts, stripScopeRE.ReplaceAllString(h, ""))
		}
	}
	if len(seedParts) > 3 {
		seedParts = seedParts[:3]
	}
	seed := strings.Join(seedParts, " ")

	scopedStripped := false
	for _, h := range hits {
		if strings.HasPrefix(h, "@") {
			if len(nonScoped) < len(hits) && len(nonScoped) > 0 {
				scopedStripped = true
			}
			break
		}
	}

	var b strings.Builder
	b.WriteString("[Browzer search guard] Detected topic(s) in this prompt: ")
	b.WriteString(strings.Join(preview, ", "))
	b.WriteString(".\n")
	b.WriteString("Before answering or writing code that touches these, run:\n")
	b.WriteString("  browzer search \"")
	b.WriteString(seed)
	b.WriteString(" <your refined question>\" --json --save /tmp/search.json\n")
	if scopedStripped {
		b.WriteString("NOTE: @-scoped package paths were stripped from the search seed — ")
		b.WriteString("translate them to natural-language concepts when refining the query.\n")
	}
	b.WriteString("The workspace docs index is authoritative for how this project uses these libraries. ")
	b.WriteString("Your training data may be stale or not match the project's version. ")
	b.WriteString("If the search returns 0 hits, say so explicitly and proceed with training-data knowledge — ")
	b.WriteString("do NOT pretend you searched when you didn't. /tmp/search.json is the receipt.\n")
	b.WriteString("To customize what this guard reacts to in a specific repo, add terms to ")
	b.WriteString(".browzer/search-triggers.json (array of strings).")
	return b.String()
}

// readStringArrayFile reads a JSON file expected to hold an array of
// strings and returns the slice. ok=false on any I/O or shape error.
func readStringArrayFile(path string) ([]string, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, false
	}
	return arr, true
}

// readExcludeFile reads the optional .browzer/search-triggers.exclude.json.
// ok=false when the file is missing or malformed; ok=true when at least
// the JSON parsed (individual fields default to nil).
func readExcludeFile(path string) (excludeFile, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return excludeFile{}, false
	}
	var ex excludeFile
	if err := json.Unmarshal(raw, &ex); err != nil {
		return excludeFile{}, false
	}
	return ex, true
}

// firstNonEmpty returns the first non-empty string from the supplied
// arguments, or "" if every argument is empty.
func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
