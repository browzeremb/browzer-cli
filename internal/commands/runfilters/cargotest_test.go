package runfilters

import (
	"strings"
	"testing"
)

const cargoTestAllOk = `running 3 tests
test math::add ... ok
test math::sub ... ok
test math::mul ... ok
test result: ok. 3 passed; 0 failed; 0 ignored; 0 measured
`

const cargoTestWithFailures = `running 3 tests
test math::add ... ok
test math::div ... FAILED
test math::mul ... ok

failures:

---- math::div stdout ----
thread 'math::div' panicked at 'attempt to divide by zero', src/math.rs:12
note: run with RUST_BACKTRACE=1 for a backtrace

failures:
    math::div

test result: FAILED. 2 passed; 1 failed; 0 ignored; 0 measured
`

const cargoTestNDJSON = `{"type":"suite","event":"started","test_count":2}
{"type":"test","event":"started","name":"math::add"}
{"type":"test","event":"ok","name":"math::add"}
{"type":"test","event":"started","name":"math::div"}
{"type":"test","event":"failed","name":"math::div","stdout":"thread panicked at divide by zero\n"}
{"type":"suite","event":"failed","passed":1,"failed":1}
`

func TestFilterCargoTest_AllOk_ResultLineOnly(t *testing.T) {
	out := filterCargoTest([]byte(cargoTestAllOk))
	s := string(out)

	// Result line must be kept.
	if !strings.Contains(s, "test result: ok") {
		t.Error("expected 'test result: ok' to be kept")
	}

	// Individual ok test lines must be dropped.
	if strings.Contains(s, "math::add ... ok") {
		t.Error("expected 'math::add ... ok' to be dropped")
	}

	// "running N tests" must be dropped.
	if strings.Contains(s, "running 3 tests") {
		t.Error("expected 'running N tests' to be dropped")
	}
}

func TestFilterCargoTest_Failures_FailedLinesAndDetails(t *testing.T) {
	out := filterCargoTest([]byte(cargoTestWithFailures))
	s := string(out)

	// FAILED test line must be kept.
	if !strings.Contains(s, "math::div ... FAILED") {
		t.Error("expected 'math::div ... FAILED' to be kept")
	}

	// failures: section must be kept.
	if !strings.Contains(s, "failures:") {
		t.Error("expected 'failures:' section to be kept")
	}

	// Panic detail must be kept.
	if !strings.Contains(s, "divide by zero") {
		t.Error("expected panic detail 'divide by zero' to be kept")
	}

	// Final result line must be kept.
	if !strings.Contains(s, "test result: FAILED") {
		t.Error("expected 'test result: FAILED' to be kept")
	}

	// Passing test lines must be dropped.
	if strings.Contains(s, "math::add ... ok") || strings.Contains(s, "math::mul ... ok") {
		t.Error("expected passing 'ok' test lines to be dropped")
	}
}

func TestFilterCargoTest_NDJSON_FailedOnly(t *testing.T) {
	out := filterCargoTest([]byte(cargoTestNDJSON))
	s := string(out)

	// Failed test must appear.
	if !strings.Contains(s, "FAILED math::div") {
		t.Error("expected 'FAILED math::div' in NDJSON output")
	}

	// Stdout detail must appear.
	if !strings.Contains(s, "divide by zero") {
		t.Error("expected failure stdout detail to be kept in NDJSON output")
	}

	// Passing test must be absent.
	if strings.Contains(s, "math::add") {
		t.Error("expected passing 'math::add' to be absent from NDJSON output")
	}
}

func TestFilterCargoTest_RegistrationKeys(t *testing.T) {
	args := []string{"cargo", "test"}
	if fn := DefaultRegistry.Resolve(args); fn == nil {
		t.Error("expected filter registered for 'cargo test'")
	}
}
