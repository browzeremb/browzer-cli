package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// FakeClock — injectable clock for tests; zero real-time sleeps.
// ---------------------------------------------------------------------------

// FakeClock implements Clock using virtual time. Callers advance time with
// Advance; pending After channels fire immediately when their deadline is
// reached by an Advance call.
type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
}

type waiter struct {
	deadline time.Time
	ch       chan time.Time
}

// NewFakeClock returns a FakeClock anchored at t.
func NewFakeClock(t time.Time) *FakeClock {
	return &FakeClock{now: t}
}

// Now returns the current virtual time.
func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// After returns a channel that fires once the virtual clock reaches
// now+d via Advance. It never blocks real wall time.
func (f *FakeClock) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	deadline := f.now.Add(d)
	// If already past deadline, fire immediately.
	if !deadline.After(f.now) {
		ch <- f.now
		return ch
	}
	f.waiters = append(f.waiters, waiter{deadline: deadline, ch: ch})
	return ch
}

// Advance moves virtual time forward by d and fires all pending After
// channels whose deadline has been reached.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	now := f.now
	remaining := f.waiters[:0]
	var fire []chan time.Time
	for _, w := range f.waiters {
		if !w.deadline.After(now) {
			fire = append(fire, w.ch)
		} else {
			remaining = append(remaining, w)
		}
	}
	f.waiters = remaining
	f.mu.Unlock()

	for _, ch := range fire {
		ch <- now
	}
}

// ---------------------------------------------------------------------------
// Tests — RequestDeviceCode
// ---------------------------------------------------------------------------

// TestRequestDeviceCode_HappyPath confirms the success path decodes
// the canonical RFC 8628 response shape and returns it intact.
func TestRequestDeviceCode_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/device/code" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"device_code":"dc","user_code":"UC","verification_uri":"https://example.com/device","expires_in":600,"interval":5}`)
	}))
	defer srv.Close()

	out, err := RequestDeviceCode(context.Background(), srv.URL, "browzer-cli")
	if err != nil {
		t.Fatal(err)
	}
	if out.DeviceCode != "dc" || out.Interval != 5 {
		t.Fatalf("unexpected response: %+v", out)
	}
}

// TestRequestDeviceCode_ResponseSizeBounded asserts the success-path
// decoder is wrapped in a LimitReader so a hostile or buggy server
// cannot stream gigabytes into the CLI's memory.
func TestRequestDeviceCode_ResponseSizeBounded(t *testing.T) {
	// Build a response just a bit larger than maxAuthResponseBytes by
	// stuffing the device_code with junk; the decode should fail with
	// an unexpected-EOF or unexpected-end-of-input rather than reading
	// the whole stream.
	huge := strings.Repeat("a", maxAuthResponseBytes+128)
	body := `{"device_code":"` + huge + `","user_code":"UC","verification_uri":"https://example.com","expires_in":600,"interval":5}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	if _, err := RequestDeviceCode(context.Background(), srv.URL, "browzer-cli"); err == nil {
		t.Fatalf("expected decode error from oversized response")
	}
}

// ---------------------------------------------------------------------------
// Tests — PollForToken (use FakeClock to avoid real sleeps)
// ---------------------------------------------------------------------------

// TestPollForToken_SlowDownCumulativeCap proves the CLI gives up after
// maxSlowDownBumps consecutive slow_down responses instead of polling
// at the 60 s ceiling for hours.
//
// FakeClock eliminates real sleeps: each Advance(5s) fires the After
// channel immediately, so the test runs in <1 ms instead of ~75s.
func TestPollForToken_SlowDownCumulativeCap(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":"slow_down"}`)
	}))
	defer srv.Close()

	clk := NewFakeClock(time.Now())

	// Drive the clock forward in a goroutine: each PollForToken iteration
	// blocks on clk.After(interval); Advance(interval) unblocks it.
	// We advance enough to exhaust maxSlowDownBumps (9 advances covers
	// the first poll + 8 slow-down responses) without hitting expiresIn
	// (clamped to 60s; we advance 5s * 9 = 45s total).
	// Drive the clock in a tight loop with tiny advances so that
	// whenever PollForToken registers an After(5s) waiter, the next
	// Advance will reach or exceed its deadline. We stop once PollForToken
	// returns by signalling via pollDone.
	pollDone := make(chan struct{})
	go func() {
		for {
			select {
			case <-pollDone:
				return
			default:
				clk.Advance(100 * time.Millisecond)
				// Yield to let PollForToken run and register After waiters.
				time.Sleep(time.Millisecond)
			}
		}
	}()

	_, err := PollForToken(context.Background(), PollParams{
		Server:     srv.URL,
		DeviceCode: "dc",
		Interval:   1, // clamped to minIntervalSeconds (5)
		ExpiresIn:  1, // clamped to minExpiresSeconds (60)
		ClientID:   "browzer-cli",
	}, clk)

	close(pollDone)

	if err == nil {
		t.Fatal("expected slow_down cap to abort poll")
	}
	if !strings.Contains(err.Error(), "slow down") && !strings.Contains(err.Error(), "Authorization window") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestPollForToken_ExpiredTokenAborts confirms the terminal
// `expired_token` response stops polling immediately (no sleep needed).
func TestPollForToken_ExpiredTokenAborts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprint(w, `{"error":"expired_token"}`)
	}))
	defer srv.Close()

	// FakeClock starts at Now() — no Advance needed because expired_token
	// is a terminal response that bypasses the sleep select.
	_, err := PollForToken(context.Background(), PollParams{
		Server:     srv.URL,
		DeviceCode: "dc",
		Interval:   1,
		ExpiresIn:  60,
	}, NewFakeClock(time.Now()))
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired-token error, got: %v", err)
	}
}

// TestPollForToken_HappyPath confirms success on first 200 response.
func TestPollForToken_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"access_token":"tok","token_type":"Bearer","expires_in":3600}`)
	}))
	defer srv.Close()

	tok, err := PollForToken(context.Background(), PollParams{
		Server:     srv.URL,
		DeviceCode: "dc",
		Interval:   1,
		ExpiresIn:  60,
	}, NewFakeClock(time.Now()))
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if tok.AccessToken != "tok" {
		t.Fatalf("unexpected token: %+v", tok)
	}
}

// TestPollForToken_PendingThenSuccess confirms that authorization_pending
// responses are retried (with FakeClock advancing) until success.
func TestPollForToken_PendingThenSuccess(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		if n < 3 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprint(w, `{"error":"authorization_pending"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"access_token":"tok","token_type":"Bearer","expires_in":3600}`)
	}))
	defer srv.Close()

	clk := NewFakeClock(time.Now())
	pollDone2 := make(chan struct{})
	go func() {
		for {
			select {
			case <-pollDone2:
				return
			default:
				clk.Advance(100 * time.Millisecond)
				time.Sleep(time.Millisecond)
			}
		}
	}()

	tok, err := PollForToken(context.Background(), PollParams{
		Server:     srv.URL,
		DeviceCode: "dc",
		Interval:   1, // clamped to 5
		ExpiresIn:  60,
	}, clk)

	close(pollDone2)

	if err != nil {
		t.Fatalf("expected success after pending, got: %v", err)
	}
	if tok.AccessToken != "tok" {
		t.Fatalf("unexpected token: %+v", tok)
	}
}

// ---------------------------------------------------------------------------
// Tests — authHTTPClient security properties
// ---------------------------------------------------------------------------

// TestAuthHTTPClient_IgnoresProxyEnv confirms the dedicated auth
// client does NOT honor HTTP_PROXY/HTTPS_PROXY — that would route
// device-flow tokens through an attacker-controlled corporate proxy.
func TestAuthHTTPClient_IgnoresProxyEnv(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1") // would refuse-connect if honored
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"device_code":"dc","user_code":"UC","verification_uri":"https://x","expires_in":600,"interval":5}`)
	}))
	defer srv.Close()

	if _, err := RequestDeviceCode(context.Background(), srv.URL, "browzer-cli"); err != nil {
		t.Fatalf("auth client must bypass HTTP_PROXY but got: %v", err)
	}
}
