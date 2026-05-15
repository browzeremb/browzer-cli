package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalhooks "github.com/browzeremb/browzer-cli/internal/hooks"
)

// mustMarshalPrompt serialises a UserPromptSubmit hook input envelope.
// session_id is set per-test to a unique value so dedup never carries
// across test cases.
func mustMarshalPrompt(t *testing.T, prompt string, opts ...func(map[string]any)) string {
	t.Helper()
	in := map[string]any{
		"prompt":     prompt,
		"session_id": t.Name(),
	}
	for _, f := range opts {
		f(in)
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// withAssistantTurn flags the input as an assistant turn.
func withAssistantTurn(in map[string]any) { in["is_assistant_turn"] = true }

// withCWD overrides the cwd field in the input envelope.
func withCWD(cwd string) func(map[string]any) {
	return func(in map[string]any) { in["cwd"] = cwd }
}

// stagePromptGuardFixture sets up an isolated HOME, workspace fixture,
// and dedup tmp dir. Returns the workspace root so individual tests can
// drop config files into it before calling promptGuardDecide.
func stagePromptGuardFixture(t *testing.T) string {
	t.Helper()
	wsRoot := setupWorkspaceFixture(t)
	// Redirect dedup state into the test's temp dir to keep tests
	// hermetic; the global tmpDirFn override is restored on cleanup.
	tmpDir := t.TempDir()
	restore := WithTmpDir(func() string { return tmpDir })
	t.Cleanup(restore)
	return wsRoot
}

func TestPromptGuard_AssistantTurn_VocabRich_Passthrough(t *testing.T) {
	stagePromptGuardFixture(t)
	input := mustMarshalPrompt(t,
		"I'm implementing fastify with drizzle and zod and need to deploy to vercel",
		withAssistantTurn,
	)
	code, env := promptGuardDecide([]byte(input))
	if code != internalhooks.ExitPassthrough {
		t.Fatalf("expected ExitPassthrough for assistant turn, got %d", code)
	}
	if env != nil {
		t.Fatalf("expected no envelope, got %+v", env)
	}
}

func TestPromptGuard_PlanMode_PtBR_QuebrarEmTarefas_Redirects(t *testing.T) {
	stagePromptGuardFixture(t)
	input := mustMarshalPrompt(t, "vamos quebrar em tarefas esse trabalho")
	code, env := promptGuardDecide([]byte(input))
	if code != internalhooks.ExitAllow {
		t.Fatalf("expected ExitAllow on plan-mode trigger, got %d", code)
	}
	if env == nil || env.HookSpecificOutput == nil {
		t.Fatalf("expected plan-mode advisory envelope")
	}
	if !strings.Contains(env.HookSpecificOutput.AdditionalContext, "browzer:generate-prd") {
		t.Fatalf("plan-mode advisory missing generate-prd hint: %q",
			env.HookSpecificOutput.AdditionalContext)
	}
	if env.HookSpecificOutput.HookEventName != "UserPromptSubmit" {
		t.Fatalf("expected UserPromptSubmit event, got %q", env.HookSpecificOutput.HookEventName)
	}
}

func TestPromptGuard_PlanMode_EN_BreakThisIntoTasks_Redirects(t *testing.T) {
	stagePromptGuardFixture(t)
	input := mustMarshalPrompt(t, "Can we break this into tasks before starting?")
	code, env := promptGuardDecide([]byte(input))
	if code != internalhooks.ExitAllow {
		t.Fatalf("expected ExitAllow on plan-mode trigger, got %d", code)
	}
	if env == nil || env.HookSpecificOutput == nil {
		t.Fatalf("expected plan-mode advisory envelope")
	}
	if !strings.Contains(env.HookSpecificOutput.AdditionalContext, "browzer:generate-task") {
		t.Fatalf("plan-mode advisory missing generate-task hint: %q",
			env.HookSpecificOutput.AdditionalContext)
	}
}

func TestPromptGuard_NoActionVerb_Passthrough(t *testing.T) {
	stagePromptGuardFixture(t)
	// "how does X render this component using react" — has react cue
	// but no action verb beyond conversational "how does X". The action-
	// verb regex requires implement/build/etc., so the guard must skip.
	input := mustMarshalPrompt(t, "how does X render this component now react?")
	code, env := promptGuardDecide([]byte(input))
	if code != internalhooks.ExitPassthrough {
		t.Fatalf("expected ExitPassthrough without action verb, got %d (env=%+v)", code, env)
	}
}

func TestPromptGuard_ImplementingReactWithRender_EmitsAdvisory(t *testing.T) {
	stagePromptGuardFixture(t)
	input := mustMarshalPrompt(t,
		"I'm implementing a new feature with react and the render path is broken",
	)
	code, env := promptGuardDecide([]byte(input))
	if code != internalhooks.ExitAllow {
		t.Fatalf("expected ExitAllow on react+render co-occurrence, got %d", code)
	}
	if env == nil || env.HookSpecificOutput == nil {
		t.Fatalf("expected advisory envelope")
	}
	ctx := env.HookSpecificOutput.AdditionalContext
	if !strings.Contains(ctx, `"react"`) {
		t.Fatalf("expected react in hits preview: %q", ctx)
	}
	if !strings.Contains(ctx, `"render"`) {
		t.Fatalf("expected render in hits preview (co-occurrence fired): %q", ctx)
	}
}

func TestPromptGuard_RenderSolo_NoReactCue_Passthrough(t *testing.T) {
	stagePromptGuardFixture(t)
	// "how do I render to PDF" — render solo (no React/JSX/component
	// cue, no deploy cue), so the co-occurrence rule suppresses it.
	// Also no other vocab → no hits → passthrough.
	input := mustMarshalPrompt(t, "how do I implement a render to PDF script")
	code, env := promptGuardDecide([]byte(input))
	if code != internalhooks.ExitPassthrough {
		t.Fatalf("expected ExitPassthrough for render-solo, got %d (env=%+v)", code, env)
	}
}

func TestPromptGuard_ExcludeFile_SuppressesByKeyword(t *testing.T) {
	wsRoot := stagePromptGuardFixture(t)
	exclPath := filepath.Join(wsRoot, ".browzer", "search-triggers.exclude.json")
	body := `{"exclude_keywords": ["sentry"]}`
	if err := os.WriteFile(exclPath, []byte(body), 0o600); err != nil {
		t.Fatalf("write exclude file: %v", err)
	}
	input := mustMarshalPrompt(t,
		"I'm configuring sentry for error reporting in production",
		withCWD(wsRoot),
	)
	code, env := promptGuardDecide([]byte(input))
	if code != internalhooks.ExitPassthrough {
		t.Fatalf("expected ExitPassthrough when exclude keyword matches, got %d", code)
	}
	if env != nil {
		t.Fatalf("expected no envelope when suppressed by exclude file")
	}
}

func TestPromptGuard_SlashCommand_Passthrough(t *testing.T) {
	stagePromptGuardFixture(t)
	input := mustMarshalPrompt(t, "/help fastify drizzle react vercel")
	code, env := promptGuardDecide([]byte(input))
	if code != internalhooks.ExitPassthrough {
		t.Fatalf("expected ExitPassthrough for slash command, got %d", code)
	}
	if env != nil {
		t.Fatalf("expected no envelope for slash command")
	}
}

func TestPromptGuard_ShortPrompt_Passthrough(t *testing.T) {
	stagePromptGuardFixture(t)
	input := mustMarshalPrompt(t, "hi!!")
	code, env := promptGuardDecide([]byte(input))
	if code != internalhooks.ExitPassthrough {
		t.Fatalf("expected ExitPassthrough for short prompt, got %d", code)
	}
	if env != nil {
		t.Fatalf("expected no envelope for short prompt")
	}
}

func TestPromptGuard_AllScopedHits_Passthrough(t *testing.T) {
	stagePromptGuardFixture(t)
	// Only scoped packages match; no DefaultVocab entries fire.
	input := mustMarshalPrompt(t, "I'm implementing changes touching @browzer/shared and @browzer/core")
	code, env := promptGuardDecide([]byte(input))
	if code != internalhooks.ExitPassthrough {
		t.Fatalf("expected ExitPassthrough on all-scoped hits, got %d (env=%+v)", code, env)
	}
}

func TestPromptGuard_DedupSameSessionSameHits_Passthrough(t *testing.T) {
	stagePromptGuardFixture(t)
	in := map[string]any{
		"prompt":     "I'm implementing fastify with drizzle",
		"session_id": "dedup-test-session",
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	code1, env1 := promptGuardDecide(raw)
	if code1 != internalhooks.ExitAllow || env1 == nil {
		t.Fatalf("first call: expected advisory, got code=%d env=%+v", code1, env1)
	}
	// Same session, same prompt → same fingerprint → dedup fires.
	code2, env2 := promptGuardDecide(raw)
	if code2 != internalhooks.ExitPassthrough || env2 != nil {
		t.Fatalf("second call: expected dedup passthrough, got code=%d env=%+v", code2, env2)
	}
}

func TestPromptGuard_HitsFingerprint_StableUnderReorder(t *testing.T) {
	a := hitsFingerprint([]string{"fastify", "drizzle", "react"})
	b := hitsFingerprint([]string{"react", "fastify", "drizzle"})
	if a != b {
		t.Fatalf("fingerprint should be order-independent, got %q vs %q", a, b)
	}
	if len(a) != 16 {
		t.Fatalf("fingerprint should be 16 hex chars, got %d (%q)", len(a), a)
	}
}

func TestPromptGuard_CollectHits_ImportSourceSkipsRelative(t *testing.T) {
	hits := collectHits(`use this: import x from './local'; import y from 'fastify';`, DefaultVocab())
	hasFastify := false
	for _, h := range hits {
		if h == "./local" {
			t.Fatalf("relative import should be skipped, got hit %q", h)
		}
		if h == "fastify" {
			hasFastify = true
		}
	}
	if !hasFastify {
		t.Fatalf("expected fastify hit, got %v", hits)
	}
}

// TestCollectHits_StaticVocab_CompilesOnce pins FR-01 / AC-01: the unified
// single-token regex is built at most once per process lifetime regardless
// of how many times collectHits runs. Pre-refactor each call paid ~140
// `regexp.Compile` invocations; post-refactor the count must stay at 1.
func TestCollectHits_StaticVocab_CompilesOnce(t *testing.T) {
	// Warm the cache: any call exercising the static vocab path triggers
	// the OnceValue. Using a vocab term lets us confirm collectHits hits
	// the unified regex (not just allocates the builder).
	_ = collectHits("implementing fastify with drizzle and zod", DefaultVocab())
	base := UnifiedVocabCompileCount()
	if base != 1 {
		t.Fatalf("expected compile count == 1 after warm, got %d", base)
	}
	vocab := DefaultVocab()
	for i := 0; i < 1000; i++ {
		// Each prompt is unique so the cache cannot satisfy via memoised
		// per-prompt result (collectHits has none — uniqueness only proves
		// the regex itself is reused).
		prompt := fmt.Sprintf("call %d: build something with react, deploy to vercel, ship", i)
		_ = collectHits(prompt, vocab)
	}
	if got := UnifiedVocabCompileCount(); got != base {
		t.Fatalf("regex recompiled during 1000 collectHits calls: count went %d → %d", base, got)
	}
}

// TestCollectHits_OperatorExtension_NotInStaticSet exercises the branch
// where vocab carries operator-supplied terms that defaultVocab does not
// — those must still be detected even though the unified regex does not
// cover them.
func TestCollectHits_OperatorExtension_NotInStaticSet(t *testing.T) {
	vocab := append(DefaultVocab(), "tatersaurus")
	hits := collectHits("we are switching to tatersaurus for compaction", vocab)
	found := false
	for _, h := range hits {
		if h == "tatersaurus" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("operator-extension term not detected; hits=%v", hits)
	}
}

// TestDedupSessionReminder_SentinelFastPath pins FR-02 / AC-02: first call
// for a given (sessionID, fingerprint) returns false and creates the
// sentinel file; the second call returns true; no JSON ledger is ever
// written.
func TestDedupSessionReminder_SentinelFastPath(t *testing.T) {
	tmpDir := t.TempDir()
	restore := WithTmpDir(func() string { return tmpDir })
	t.Cleanup(restore)

	if first := dedupSessionReminder("session-A", "fp-123"); first != false {
		t.Fatalf("first call: expected false, got true")
	}
	sentinel := filepath.Join(tmpDir, ".browzer-guard", "session-A", "fp-123")
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel file missing after first call: %v", err)
	}
	// Legacy JSON ledger filename — must NOT be written by the fast path.
	legacyJSON := filepath.Join(tmpDir, ".browzer-guard", "session-A.json")
	if _, err := os.Stat(legacyJSON); err == nil {
		t.Fatalf("unexpected JSON ledger written at %s", legacyJSON)
	}
	if second := dedupSessionReminder("session-A", "fp-123"); second != true {
		t.Fatalf("second call: expected true, got false")
	}
}

// TestDedupSessionReminder_DistinctFingerprintsIndependent confirms two
// different fingerprints under the same session ID both fire their own
// first-emission decision — the sentinel layout keys on the pair, not
// the session alone.
func TestDedupSessionReminder_DistinctFingerprintsIndependent(t *testing.T) {
	tmpDir := t.TempDir()
	restore := WithTmpDir(func() string { return tmpDir })
	t.Cleanup(restore)

	if r := dedupSessionReminder("session-B", "fp-1"); r != false {
		t.Fatalf("first call fp-1: expected false, got %v", r)
	}
	if r := dedupSessionReminder("session-B", "fp-2"); r != false {
		t.Fatalf("first call fp-2: expected false (distinct fingerprint), got %v", r)
	}
	if r := dedupSessionReminder("session-B", "fp-1"); r != true {
		t.Fatalf("repeat fp-1: expected true, got %v", r)
	}
}
