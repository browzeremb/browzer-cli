package ui

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// Spinner is a minimal stdout spinner for long-running CLI steps. It
// deliberately avoids bubbletea so the CLI can keep printing regular
// lines around it — bubbletea takes over the screen, which breaks the
// "Walking... ✓ Parsing... ✓" line-oriented flow that init/sync use.
//
// Usage:
//
//	sp := ui.StartSpinner("Parsing code on server...")
//	// ... do work ...
//	sp.Success("Parsed 438 files")   // clears spinner, prints ✓ line
//	// or sp.Stop()                  // clears spinner, no replacement
//	// or sp.Failure("Parse failed")
//
// Concurrency: Start/Stop are safe from any goroutine. Calling Stop
// twice is a no-op.
//
// Fallbacks:
//   - If stdout is not a TTY (or NO_COLOR is set), StartSpinner prints
//     the label plainly (no animation, no ANSI) and the later
//     Success/Failure/Stop calls print their replacement line directly.
//     This is what keeps `browzer ... --json | jq` clean: no cursor
//     tricks, no hidden escape sequences.
type Spinner struct {
	label    string
	mu       sync.Mutex
	stopCh   chan struct{}
	doneCh   chan struct{}
	stopped  bool
	animated bool
}

// Braille frames — same sequence used by charmbracelet/spinner.Dot,
// chosen because they render identically across Terminal.app, iTerm2,
// Alacritty, Ghostty and Windows Terminal.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// StartSpinner writes label to stdout and begins animating a braille
// frame in-place if the stream is a TTY. Returns a handle the caller
// MUST terminate with Success/Failure/Stop — leaving the goroutine
// running leaks a ticker and hangs the cursor.
func StartSpinner(label string) *Spinner {
	s := &Spinner{
		label:    label,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		animated: colorEnabled(),
	}

	if !s.animated {
		fmt.Printf("  %s ", label)
		// doneCh is closed eagerly so Stop() has nothing to wait on.
		close(s.doneCh)
		return s
	}

	go s.run()
	return s
}

func (s *Spinner) run() {
	defer close(s.doneCh)

	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()

	i := 0
	for {
		// "\r\033[K" = return to col 0 + clear line. Drawing the
		// frame with styleSuccess keeps the spinner in brand green.
		fmt.Fprintf(os.Stdout, "\r\033[K  %s %s",
			styleSuccess.Render(spinnerFrames[i]),
			styleBody.Render(s.label),
		)
		i = (i + 1) % len(spinnerFrames)

		select {
		case <-s.stopCh:
			// Erase the spinner line so the caller can print its
			// replacement cleanly.
			fmt.Fprint(os.Stdout, "\r\033[K")
			return
		case <-ticker.C:
		}
	}
}

// Success stops the spinner and replaces it with a ✓ line.
func (s *Spinner) Success(msg string) {
	s.stopAnimation()
	Success(msg)
}

// Failure stops the spinner and replaces it with a ✗ line (on stderr).
func (s *Spinner) Failure(msg string) {
	s.stopAnimation()
	Failure(msg)
}

// Stop halts the spinner without printing a replacement line.
func (s *Spinner) Stop() {
	s.stopAnimation()
	if !s.animated {
		// Non-animated branch already printed the label on start;
		// terminate the line so the next print starts on a fresh row.
		fmt.Println()
	}
}

// stopAnimation is the idempotent internal helper that shuts down the
// ticker goroutine exactly once.
func (s *Spinner) stopAnimation() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	s.mu.Unlock()

	if s.animated {
		close(s.stopCh)
		<-s.doneCh
	}
}
