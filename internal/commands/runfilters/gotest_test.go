package runfilters

import (
	"strings"
	"testing"
)

const goTestStandardFail = `--- PASS: TestSum (0.00s)
--- FAIL: TestDiv (0.00s)
    divide_test.go:22: expected 2, got 0
--- PASS: TestMul (0.00s)
FAIL
FAIL	example.com/mymod/math	0.012s
ok  	example.com/mymod/util	0.005s
`

const goTestStandardAllPass = `--- PASS: TestSum (0.00s)
--- PASS: TestMul (0.00s)
ok  	example.com/mymod/math	0.005s
ok  	example.com/mymod/util	0.003s
`

const goTestNDJSONFail = `{"Action":"run","Package":"example.com/mymod","Test":"TestA"}
{"Action":"output","Package":"example.com/mymod","Test":"TestA","Output":"--- PASS: TestA (0.00s)\n"}
{"Action":"pass","Package":"example.com/mymod","Test":"TestA","Elapsed":0.001}
{"Action":"run","Package":"example.com/mymod","Test":"TestB"}
{"Action":"output","Package":"example.com/mymod","Test":"TestB","Output":"--- FAIL: TestB (0.00s)\n"}
{"Action":"output","Package":"example.com/mymod","Test":"TestB","Output":"    foo_test.go:10: Error: expected true\n"}
{"Action":"fail","Package":"example.com/mymod","Test":"TestB","Elapsed":0.002}
{"Action":"fail","Package":"example.com/mymod","Elapsed":0.003}
`

func TestFilterGoTest_StandardFail_Filtered(t *testing.T) {
	out := filterGoTest([]byte(goTestStandardFail))
	s := string(out)

	// FAIL lines must be kept.
	if !strings.Contains(s, "--- FAIL: TestDiv") {
		t.Error("expected '--- FAIL: TestDiv' to be kept")
	}
	if !strings.Contains(s, "FAIL\texample.com/mymod/math") {
		t.Error("expected package-level FAIL line to be kept")
	}

	// Error detail must be kept.
	if !strings.Contains(s, "divide_test.go:22") {
		t.Error("expected error detail line to be kept")
	}

	// PASS lines must be dropped.
	if strings.Contains(s, "--- PASS: TestSum") {
		t.Error("expected '--- PASS: TestSum' to be dropped")
	}

	// ok package lines must be dropped.
	if strings.Contains(s, "ok  \texample.com/mymod/util") {
		t.Error("expected 'ok' package line to be dropped")
	}
}

func TestFilterGoTest_StandardAllPass_Empty(t *testing.T) {
	out := filterGoTest([]byte(goTestStandardAllPass))
	s := string(out)

	// No PASS/ok lines should survive.
	if strings.Contains(s, "--- PASS") {
		t.Error("expected '--- PASS' lines to be dropped")
	}
	if strings.Contains(s, "ok  ") {
		t.Error("expected 'ok  ' lines to be dropped")
	}
}

func TestFilterGoTest_NDJSONFail_CompactOutput(t *testing.T) {
	out := filterGoTest([]byte(goTestNDJSONFail))
	s := string(out)

	// Failing test must appear as compact FAIL line.
	if !strings.Contains(s, "FAIL TestB") {
		t.Error("expected compact 'FAIL TestB' line in NDJSON output")
	}

	// Error output line must be kept.
	if !strings.Contains(s, "Error: expected true") {
		t.Error("expected error output line to be kept from NDJSON")
	}

	// Passing test must not appear.
	if strings.Contains(s, "TestA") {
		t.Error("expected passing test 'TestA' to be absent from NDJSON output")
	}
}

// goTestNDJSONNonKeywordBody exercises FR-07 fix: output without FAIL/Error
// tokens (e.g. cmp.Diff result) must still be emitted for failed tests.
const goTestNDJSONNonKeywordBody = `{"Action":"run","Package":"example.com/mymod","Test":"TestDiff"}
{"Action":"output","Package":"example.com/mymod","Test":"TestDiff","Output":"    foo_test.go:42: mismatch (-want +got):\n"}
{"Action":"output","Package":"example.com/mymod","Test":"TestDiff","Output":"      string(\n"}
{"Action":"output","Package":"example.com/mymod","Test":"TestDiff","Output":"-         \"hello\"\n"}
{"Action":"output","Package":"example.com/mymod","Test":"TestDiff","Output":"+         \"world\"\n"}
{"Action":"output","Package":"example.com/mymod","Test":"TestDiff","Output":"      )\n"}
{"Action":"fail","Package":"example.com/mymod","Test":"TestDiff","Elapsed":0.001}
{"Action":"fail","Package":"example.com/mymod","Elapsed":0.002}
`

func TestFilterGoTest_NDJSONNonKeywordBody_Retained(t *testing.T) {
	// FR-07: cmp.Diff lines contain no FAIL/Error tokens but must still appear
	// because the test they belong to failed.
	out := filterGoTest([]byte(goTestNDJSONNonKeywordBody))
	s := string(out)

	if !strings.Contains(s, "FAIL TestDiff") {
		t.Error("expected compact FAIL line for TestDiff")
	}
	if !strings.Contains(s, "mismatch (-want +got)") {
		t.Error("expected cmp.Diff header line to be retained for failed test")
	}
	if !strings.Contains(s, `"hello"`) {
		t.Error("expected cmp.Diff -want line to be retained for failed test")
	}
	if !strings.Contains(s, `"world"`) {
		t.Error("expected cmp.Diff +got line to be retained for failed test")
	}
}

func TestFilterGoTest_NDJSONPassingBodyDropped(t *testing.T) {
	// Output belonging to a passing test must not appear.
	out := filterGoTest([]byte(goTestNDJSONFail))
	s := string(out)

	if strings.Contains(s, "--- PASS: TestA") {
		t.Error("expected passing test body to be dropped")
	}
}

func TestFilterGoTest_RegistrationKeys(t *testing.T) {
	args := []string{"go", "test"}
	if fn := DefaultRegistry.Resolve(args); fn == nil {
		t.Error("expected filter registered for 'go test'")
	}
}
