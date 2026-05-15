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
	"sync"
	"sync/atomic"

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

// tmpDirFn lets tests override the dedup-state directory location.
var tmpDirFn = os.TempDir

// unifiedVocabCompileCounter records how many times the unified
// single-token regex has been built. The wrapper closure below increments
// it; tests assert it stays at 1 after thousands of collectHits calls to
// pin the "compile once per process" contract (FR-01 / AC-01).
var unifiedVocabCompileCounter atomic.Int64

// unifiedSingleTokenRE compiles a single (?i)\b(t1|t2|...)\b regex out of
// every single-token entry in defaultVocab + cooccurrenceVocab.Term.
// Multi-token entries (containing space, slash, or dot) stay on the
// strings.Contains path in collectHits — \b cannot mark word-boundaries
// inside them. sync.OnceValue is lazy: processes that never reach
// collectHits (assistant-turn passthrough, short prompts, slash-command
// prompts) skip the construction entirely.
var unifiedSingleTokenRE = sync.OnceValue(func() *regexp.Regexp {
	unifiedVocabCompileCounter.Add(1)
	parts := make([]string, 0, len(defaultVocab)+len(cooccurrenceVocab))
	for _, t := range defaultVocab {
		if !strings.ContainsAny(t, " /.") {
			parts = append(parts, regexp.QuoteMeta(t))
		}
	}
	for _, r := range cooccurrenceVocab {
		if !strings.ContainsAny(r.Term, " /.") {
			parts = append(parts, regexp.QuoteMeta(r.Term))
		}
	}
	if len(parts) == 0 {
		// Caller treats nil as "no static vocab terms to match";
		// avoids the need for a RE2-incompatible never-matching
		// pattern.
		return nil
	}
	return regexp.MustCompile(`(?i)\b(?:` + strings.Join(parts, "|") + `)\b`)
})

// unifiedSingleTokenIndex maps the lowercased form of every static
// single-token vocab entry back to its canonical (original-case) form,
// so collectHits can normalize the regex's case-insensitive matches into
// the preview strings the operator sees.
var unifiedSingleTokenIndex = sync.OnceValue(func() map[string]string {
	idx := make(map[string]string, len(defaultVocab)+len(cooccurrenceVocab))
	for _, t := range defaultVocab {
		if !strings.ContainsAny(t, " /.") {
			idx[strings.ToLower(t)] = t
		}
	}
	for _, r := range cooccurrenceVocab {
		if !strings.ContainsAny(r.Term, " /.") {
			idx[strings.ToLower(r.Term)] = r.Term
		}
	}
	return idx
})

// defaultVocabSingleTokenSet is the lookup the collectHits operator-extension
// branch consults so terms covered by the unified regex are not re-compiled
// per call. Built lazily for symmetry with the regex itself.
var defaultVocabSingleTokenSet = sync.OnceValue(func() map[string]struct{} {
	set := make(map[string]struct{}, len(defaultVocab)+len(cooccurrenceVocab))
	for _, t := range defaultVocab {
		if !strings.ContainsAny(t, " /.") {
			set[strings.ToLower(t)] = struct{}{}
		}
	}
	for _, r := range cooccurrenceVocab {
		if !strings.ContainsAny(r.Term, " /.") {
			set[strings.ToLower(r.Term)] = struct{}{}
		}
	}
	return set
})

// compiledExclude holds the parsed + pre-compiled .browzer/search-triggers.exclude.json
// content. Keywords are lowercased once at load time; regex patterns are compiled
// once. The whole struct is cached in compiledExcludeCache by file path so repeated
// calls in the same process pay no recompile cost.
type compiledExclude struct {
	keywords []string         // already lowercased
	regexes  []*regexp.Regexp // compiled with `(?i)` prefix
}

// compiledExcludeCache memoises loadCompiledExclude by file path so the
// regex compile path is exercised at most once per (process, path) pair
// — satisfying FR-01's "exclude regex compiled at most once per process".
// A nil value in the map means "file absent / malformed; do not retry".
var compiledExcludeCache sync.Map // key: string -> value: *compiledExclude

// loadCompiledExclude reads + parses + compiles the exclude file at `path`
// the first time it is called for that path; subsequent calls in the same
// process return the cached value. Returns nil when the file is missing
// or malformed (treated as "no exclude rules").
func loadCompiledExclude(path string) *compiledExclude {
	if v, ok := compiledExcludeCache.Load(path); ok {
		return v.(*compiledExclude)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		compiledExcludeCache.Store(path, (*compiledExclude)(nil))
		return nil
	}
	var ex excludeFile
	if err := json.Unmarshal(raw, &ex); err != nil {
		compiledExcludeCache.Store(path, (*compiledExclude)(nil))
		return nil
	}
	ce := &compiledExclude{
		keywords: make([]string, 0, len(ex.Keywords)),
		regexes:  make([]*regexp.Regexp, 0, len(ex.Patterns)),
	}
	for _, k := range ex.Keywords {
		ce.keywords = append(ce.keywords, strings.ToLower(k))
	}
	for _, src := range ex.Patterns {
		re, err := regexp.Compile(`(?i)` + src)
		if err != nil {
			continue
		}
		ce.regexes = append(ce.regexes, re)
	}
	compiledExcludeCache.Store(path, ce)
	return ce
}

// UnifiedVocabCompileCount returns the number of times the unified
// single-token regex has been built across this process's lifetime.
// Test seam for the FR-01 contract: collectHits must trigger at most
// one compile no matter how many times it is invoked.
func UnifiedVocabCompileCount() int64 { return unifiedVocabCompileCounter.Load() }

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
	// Both the keyword list (lowercased) and the pattern list (compiled) are
	// cached per-process by loadCompiledExclude so the hot path performs zero
	// regex compiles after the first prompt warms the cache.
	if excl := loadCompiledExclude(filepath.Join(cwd, ".browzer", "search-triggers.exclude.json")); excl != nil {
		lower := strings.ToLower(prompt)
		for _, k := range excl.keywords {
			if strings.Contains(lower, k) {
				return internalhooks.ExitPassthrough, nil
			}
		}
		for _, re := range excl.regexes {
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
//
// The static vocab (defaultVocab + cooccurrenceVocab.Term) is matched in a
// single FindAllString pass against the unified word-boundary regex built
// once per process by unifiedSingleTokenRE. Multi-token vocab entries and
// operator-supplied extensions (vocab params not already in the static
// set) keep the per-term path; the static set covers ~90% of the vocab.
func collectHits(prompt string, vocab []string) []string {
	seen := make(map[string]struct{}, 16)
	add := func(s string) {
		if s == "" {
			return
		}
		seen[s] = struct{}{}
	}

	lower := strings.ToLower(prompt)
	staticSet := defaultVocabSingleTokenSet()

	// Multi-token vocab + operator-extension single tokens: per-term path.
	// Static single-token defaultVocab + cooccurrenceVocab entries are
	// covered by the unified regex below, so we skip them here.
	for _, term := range vocab {
		t := strings.ToLower(term)
		if strings.ContainsAny(t, " /.") {
			if strings.Contains(lower, t) {
				add(term)
			}
			continue
		}
		if _, isStatic := staticSet[t]; isStatic {
			continue
		}
		// Operator-supplied extension (term not in defaultVocab); the
		// extension set is typically tiny (≤ 5 entries), so per-call
		// compilation is acceptable.
		re, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(t) + `\b`)
		if err != nil {
			continue
		}
		if re.MatchString(prompt) {
			add(term)
		}
	}

	// Static single-token sweep: one regex, one FindAllString call,
	// normalized back to canonical case via unifiedSingleTokenIndex.
	// nil regex means the static vocab set is empty (e.g. a future
	// build that strips defaultVocab to zero entries) — the loop
	// trivially produces no hits.
	if re := unifiedSingleTokenRE(); re != nil {
		idx := unifiedSingleTokenIndex()
		for _, m := range re.FindAllString(prompt, -1) {
			if canon, ok := idx[strings.ToLower(m)]; ok {
				add(canon)
			}
		}
	}

	// Co-occurrence rules: gate each static-vocab hit by its cue regex.
	// The Term match already happened in the unified sweep; we just
	// remove false positives whose cue is absent.
	for _, rule := range cooccurrenceVocab {
		if strings.ContainsAny(rule.Term, " /.") {
			// Multi-token co-occurrence terms (none today) — would need
			// the substring path above. Not used currently, but the
			// branch keeps the table forward-compatible.
			continue
		}
		if _, hit := seen[rule.Term]; !hit {
			continue
		}
		if !rule.RequiresRE.MatchString(prompt) {
			delete(seen, rule.Term)
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

	// Return in stable order: sort the deduplicated set so fingerprints
	// and preview strings are reproducible.
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

// dedupSessionReminder is a once-per-(sessionID, fingerprint) emission
// gate. First call returns false (caller emits the advisory); subsequent
// calls with the same (sessionID, fingerprint) return true (caller
// suppresses). The state lives in a sentinel file
// `$TMPDIR/.browzer-guard/<sessionID>/<fingerprint>` created via
// `O_CREATE|O_EXCL|O_WRONLY` — the canonical atomic-create idiom that
// session_sentinel.go already uses for the same purpose.
//
// Best-effort semantics preserved: any unexpected I/O failure returns
// false so a real prompt is never silently swallowed (err toward
// emission). EEXIST is the dedup signal.
func dedupSessionReminder(sessionID, fingerprint string) bool {
	if sessionID == "" {
		return false
	}
	dir := filepath.Join(tmpDirFn(), ".browzer-guard", sessionID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false
	}
	path := filepath.Join(dir, fingerprint)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		_ = f.Close()
		return false
	}
	if os.IsExist(err) {
		return true
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
