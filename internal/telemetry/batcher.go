package telemetry

import (
	"context"
	"time"

	"github.com/browzeremb/browzer-cli/internal/tracker"
)

// BatcherOptions configures the periodic flush.
type BatcherOptions struct {
	// Interval between flush attempts. Default 5 minutes.
	Interval time.Duration
	// Threshold of unsent events that triggers an immediate flush regardless
	// of Interval. Default 100.
	Threshold int
}

// SendFn delivers buckets to the server. Called inside the batcher.
// Returning an error keeps the events unsent for the next attempt.
type SendFn func(ctx context.Context, buckets []tracker.Bucket) error

// Batcher periodically aggregates and flushes unsent events.
type Batcher struct {
	tr   *tracker.Tracker
	send SendFn
	opts BatcherOptions
}

// NewBatcher constructs a batcher.
//
// TODO(follow-up): Wire into daemon_cmd.go's daemonStartCmd().RunE after
// the Daemon PR (feat/cli-token-economy-daemon) merges. The wiring should
// gate on creds.TelemetryConsentAt != nil before starting the goroutine.
// See Task 2 / Task 5.3 in docs/superpowers/plans/2026-04-15-cli-token-economy-tracking.md.
func NewBatcher(tr *tracker.Tracker, send SendFn, opts BatcherOptions) *Batcher {
	if opts.Interval == 0 {
		opts.Interval = 5 * time.Minute
	}
	if opts.Threshold == 0 {
		opts.Threshold = 100
	}
	return &Batcher{tr: tr, send: send, opts: opts}
}

// Run loops until ctx is canceled. Blocks.
func (b *Batcher) Run(ctx context.Context) {
	t := time.NewTicker(b.opts.Interval / 4)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			b.tryFlush(ctx)
		}
	}
}

func (b *Batcher) tryFlush(ctx context.Context) {
	buckets, ids, err := b.tr.UnsentBuckets()
	if err != nil || len(buckets) == 0 {
		return
	}
	if err := b.send(ctx, buckets); err != nil {
		return // keep events for next attempt
	}
	_ = b.tr.MarkFlushed(ids)
}
