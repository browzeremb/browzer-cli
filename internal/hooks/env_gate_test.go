package hooks

import "testing"

func TestGateAllowed_DefaultOn(t *testing.T) {
	t.Setenv("BROWZER_HOOK", "")
	t.Setenv("BROWZER_HOOK_DISABLE", "")
	if !GateAllowed("rewrite-bash") {
		t.Fatalf("default should allow when env vars unset")
	}
}

func TestGateAllowed_OffKillsAll(t *testing.T) {
	for _, val := range []string{"off", "OFF", "0", "false", "FALSE", " off "} {
		t.Setenv("BROWZER_HOOK", val)
		if GateAllowed("rewrite-bash") {
			t.Fatalf("BROWZER_HOOK=%q should disable all hooks", val)
		}
	}
}

func TestGateAllowed_OtherValuesAllow(t *testing.T) {
	for _, val := range []string{"on", "1", "true", "yes"} {
		t.Setenv("BROWZER_HOOK", val)
		t.Setenv("BROWZER_HOOK_DISABLE", "")
		if !GateAllowed("rewrite-bash") {
			t.Fatalf("BROWZER_HOOK=%q should NOT disable hooks", val)
		}
	}
}

func TestGateAllowed_DisableCSVMatch(t *testing.T) {
	t.Setenv("BROWZER_HOOK", "")
	t.Setenv("BROWZER_HOOK_DISABLE", "rewrite-bash,session-start")
	if GateAllowed("rewrite-bash") {
		t.Fatalf("rewrite-bash should be disabled via CSV")
	}
	if GateAllowed("session-start") {
		t.Fatalf("session-start should be disabled via CSV")
	}
	if !GateAllowed("rewrite-read") {
		t.Fatalf("rewrite-read NOT in CSV should remain allowed")
	}
}

func TestGateAllowed_DisableCSVWhitespaceTolerant(t *testing.T) {
	t.Setenv("BROWZER_HOOK", "")
	t.Setenv("BROWZER_HOOK_DISABLE", "  rewrite-bash , , session-start  ,  ")
	if GateAllowed("rewrite-bash") {
		t.Fatalf("whitespace-padded CSV entries should still match")
	}
	if GateAllowed("session-start") {
		t.Fatalf("whitespace-padded CSV entries should still match")
	}
	if !GateAllowed("postuse-run") {
		t.Fatalf("unrelated hook should pass")
	}
}

func TestGateAllowed_DisableCSVCaseInsensitive(t *testing.T) {
	t.Setenv("BROWZER_HOOK", "")
	t.Setenv("BROWZER_HOOK_DISABLE", "Rewrite-Bash")
	if GateAllowed("rewrite-bash") {
		t.Fatalf("CSV entries should match case-insensitively")
	}
	if GateAllowed("REWRITE-BASH") {
		t.Fatalf("hookName argument should match case-insensitively")
	}
}

func TestGateAllowed_OffWinsOverCSV(t *testing.T) {
	t.Setenv("BROWZER_HOOK", "off")
	t.Setenv("BROWZER_HOOK_DISABLE", "")
	if GateAllowed("anything") {
		t.Fatalf("BROWZER_HOOK=off must short-circuit before CSV evaluation")
	}
}

func TestGateAllowed_EmptyHookName(t *testing.T) {
	t.Setenv("BROWZER_HOOK", "")
	t.Setenv("BROWZER_HOOK_DISABLE", "rewrite-bash")
	// Empty hookName means "no granular identity" — still allowed
	// unless BROWZER_HOOK kills the whole surface.
	if !GateAllowed("") {
		t.Fatalf("empty hookName should bypass CSV check and remain allowed")
	}
	t.Setenv("BROWZER_HOOK", "off")
	if GateAllowed("") {
		t.Fatalf("BROWZER_HOOK=off must disable even empty hookName")
	}
}
