package runfilters

import (
	"testing"
)

func TestRegistry_Resolve_SingleVerb(t *testing.T) {
	r := New()
	called := false
	r.Register("git", func(stdout []byte) []byte {
		called = true
		return stdout
	})

	fn := r.Resolve([]string{"git", "status"})
	if fn == nil {
		t.Fatal("expected non-nil FilterFn for 'git status'")
	}
	fn(nil)
	if !called {
		t.Error("expected registered filter to be called")
	}
}

func TestRegistry_Resolve_TwoWordKeyWinsOverSingleVerb(t *testing.T) {
	r := New()
	var called string
	r.Register("pnpm", func(stdout []byte) []byte {
		called = "pnpm"
		return stdout
	})
	r.Register("pnpm turbo", func(stdout []byte) []byte {
		called = "pnpm turbo"
		return stdout
	})

	fn := r.Resolve([]string{"pnpm", "turbo", "test"})
	if fn == nil {
		t.Fatal("expected non-nil FilterFn for 'pnpm turbo test'")
	}
	fn(nil)
	if called != "pnpm turbo" {
		t.Errorf("expected 'pnpm turbo' filter, got %q", called)
	}
}

func TestRegistry_Resolve_FallsBackToSingleVerbWhenNoTwoWordMatch(t *testing.T) {
	r := New()
	var called string
	r.Register("pnpm", func(stdout []byte) []byte {
		called = "pnpm"
		return stdout
	})

	fn := r.Resolve([]string{"pnpm", "install"})
	if fn == nil {
		t.Fatal("expected non-nil FilterFn for 'pnpm install' (fallback to 'pnpm')")
	}
	fn(nil)
	if called != "pnpm" {
		t.Errorf("expected 'pnpm' filter, got %q", called)
	}
}

func TestRegistry_Resolve_UnknownCommandReturnsNil(t *testing.T) {
	r := New()
	fn := r.Resolve([]string{"unknown-cmd", "--flag"})
	if fn != nil {
		t.Error("expected nil FilterFn for unregistered command")
	}
}

func TestRegistry_Resolve_EmptyArgsReturnsNil(t *testing.T) {
	r := New()
	fn := r.Resolve([]string{})
	if fn != nil {
		t.Error("expected nil FilterFn for empty args")
	}
}

func TestRegistry_CategoryOf_TwoWordKey(t *testing.T) {
	r := New()
	r.Register("go test", func(stdout []byte) []byte { return stdout })

	got := r.CategoryOf([]string{"go", "test", "./..."})
	if got != "go-test" {
		t.Errorf("expected 'go-test', got %q", got)
	}
}

func TestRegistry_CategoryOf_SingleVerb(t *testing.T) {
	r := New()
	r.Register("vitest", func(stdout []byte) []byte { return stdout })

	got := r.CategoryOf([]string{"vitest", "--run"})
	if got != "vitest" {
		t.Errorf("expected 'vitest', got %q", got)
	}
}

func TestRegistry_CategoryOf_UnknownFallsBackToArgv0(t *testing.T) {
	r := New()
	got := r.CategoryOf([]string{"docker", "compose", "up"})
	if got != "docker" {
		t.Errorf("expected 'docker', got %q", got)
	}
}

func TestRegistry_CategoryOf_EmptyArgs(t *testing.T) {
	r := New()
	got := r.CategoryOf([]string{})
	if got != "unknown" {
		t.Errorf("expected 'unknown', got %q", got)
	}
}

func TestTrackRun_AlwaysReturnsNil(t *testing.T) {
	// TrackRun is best-effort: daemon errors are silently discarded and
	// the function always returns nil regardless of whether the daemon is
	// reachable. This test runs without a live daemon to verify the
	// return-nil contract even on dial failure.
	err := TrackRun("browzer-run-passthrough", []byte("raw output bytes"), []byte("compressed"), "git", 10)
	if err != nil {
		t.Errorf("expected TrackRun to return nil, got %v", err)
	}
}
