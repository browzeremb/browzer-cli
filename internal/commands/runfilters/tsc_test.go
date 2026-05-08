package runfilters

import (
	"strings"
	"testing"
)

func TestFilterTsc_NonPretty_TwoErrors(t *testing.T) {
	input := `src/foo.ts(10,5): error TS2304: Cannot find name 'foo'.
src/bar.ts(20,1): error TS2345: Argument of type 'string' is not assignable to parameter of type 'number'.
Found 2 errors.
`
	got := string(filterTsc([]byte(input)))

	if !strings.Contains(got, "src/foo.ts(10,5): error TS2304:") {
		t.Errorf("expected first error line in output, got: %q", got)
	}
	if !strings.Contains(got, "src/bar.ts(20,1): error TS2345:") {
		t.Errorf("expected second error line in output, got: %q", got)
	}
	if !strings.Contains(got, "Found 2 errors.") {
		t.Errorf("expected summary line in output, got: %q", got)
	}
}

func TestFilterTsc_PrettyMode(t *testing.T) {
	input := `src/foo.ts:10:5 - error TS2304: Cannot find name 'foo'.

10   const x = foo();
              ~~~

Found 1 error.
`
	got := string(filterTsc([]byte(input)))

	// Header line must be kept.
	if !strings.Contains(got, "src/foo.ts:10:5 - error TS2304:") {
		t.Errorf("expected pretty header line in output, got: %q", got)
	}
	// Summary must be kept.
	if !strings.Contains(got, "Found 1 error.") {
		t.Errorf("expected summary line in output, got: %q", got)
	}
	// ~~~ underline must be dropped.
	if strings.Contains(got, "~~~") {
		t.Errorf("expected ~~~ underlines to be dropped, got: %q", got)
	}
	// Context source line must be dropped.
	if strings.Contains(got, "const x = foo()") {
		t.Errorf("expected context source line to be dropped, got: %q", got)
	}
}

func TestFilterTsc_ZeroErrors(t *testing.T) {
	input := `Found 0 errors.
`
	got := string(filterTsc([]byte(input)))

	if !strings.Contains(got, "Found 0 errors.") {
		t.Errorf("expected summary line in output, got: %q", got)
	}
}
