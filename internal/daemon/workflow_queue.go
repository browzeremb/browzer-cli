// Package daemon — workflow_queue.go
//
// Per-path FIFO dispatcher for workflow.json mutations. The daemon owns one
// pathQueue per absolute workflow.json path; each queue has a single drainer
// goroutine that holds the advisory file lock for the duration of one
// mutation, runs `workflow.ApplyAndPersist`, then yields to the next job.
//
// Design rationale:
//   - Per-path queue (not a single global queue): two unrelated workflows can
//     mutate concurrently. A global queue would serialise everything.
//   - Lazy drainer: a goroutine is spawned the first time a path sees a job,
//     and exits after queueIdleTimeout (default 30 min) of empty channel +
//     refcount==0. This keeps the daemon's goroutine count proportional to
//     active workflows, not lifetime workflows.
//   - Channel cap 64: bounded FIFO + backpressure. Cap N=64 means we tolerate
//     short bursts without blocking the JSON-RPC reader; on overflow the
//     daemon returns `queue_full` so the caller can fall back to standalone.
//   - Crash safety:
//       async jobs (`awaitDurability=false`) in flight when daemon SIGKILLs
//         silently drop. Skills tolerate this — workflow.json on disk reflects
//         the last `--await`/standalone write only.
//       sync jobs (`awaitDurability=true`) waiting on `done` see the socket
//         drop, return error, and the caller falls back to standalone (which
//         re-applies idempotently — the mutators handle "already in target
//         state" no-ops).

package daemon

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
)

// errQueueFull is returned by enqueue when the per-path channel is at capacity.
// The handler maps this to JSON-RPC error code -32010 with message "queue_full".
var errQueueFull = errors.New("queue_full")

// pathQueueCap is the FIFO capacity for a single workflow path.
// Picked to absorb a typical orchestrate-task-delivery burst (50-step
// feature dispatch) without blocking. Operator can tune via config; this
// constant is the default.
const pathQueueCap = 64

// defaultQueueIdleTimeout is how long a path-queue's drainer stays alive
// after the channel goes empty before it self-collects. 30 min keeps the
// drainer warm for the duration of a typical feature delivery.
const defaultQueueIdleTimeout = 30 * time.Minute

// mutateJob is the unit of work pushed onto a pathQueue.
type mutateJob struct {
	verb            string
	path            string
	args            wf.MutatorArgs
	awaitDurability bool
	lockTimeout     time.Duration
	writeID         string
	enqueuedAt      time.Time

	// Result fields populated by the drainer before closing done.
	result    wf.ApplyResult
	err       error
	lockHeld  time.Duration
	completed time.Time

	// done is closed once the drainer finishes (success OR error). Sync
	// callers (`awaitDurability=true`) block on this; async callers
	// (`awaitDurability=false`) return immediately after enqueue.
	done chan struct{}
}

// pathQueue is the per-path FIFO + drainer state.
type pathQueue struct {
	ch       chan *mutateJob
	lastUsed atomic.Int64 // unix nano of last enqueue or drain
	refcount atomic.Int32 // outstanding sync waiters (sync.Once-guarded by enqueue/drain)
	// drainerExited is closed when the drainer goroutine returns. Tests use
	// this to assert the idle-collector behaviour.
	drainerExited chan struct{}
}

// workflowDispatcher owns one pathQueue per active workflow path.
//
// Concurrency model:
//   - dispatcher.mu guards `queues` (map insertions/deletions only).
//   - Each pathQueue's `ch` is the synchronisation primitive between
//     enqueue (handler goroutine) and drain (worker goroutine).
//   - The drainer self-deletes its entry from `queues` on idle exit; the
//     deletion takes `mu` and double-checks `len(ch)==0 && refcount==0` to
//     avoid racing with a fresh enqueue that arrived after the timeout
//     fired but before mu was held.
type workflowDispatcher struct {
	mu               sync.Mutex
	queues           map[string]*pathQueue
	queueIdleTimeout time.Duration
	// activeDrainers exposes the live drainer count for observability and
	// tests. Incremented when a drainer starts, decremented on exit.
	activeDrainers atomic.Int32
}

// newWorkflowDispatcher builds a dispatcher. queueIdleTimeout=0 falls back
// to defaultQueueIdleTimeout. Pass a non-zero override (typically the value
// of `daemon.workflow_keepalive_seconds`) from the daemon entrypoint.
func newWorkflowDispatcher(queueIdleTimeout time.Duration) *workflowDispatcher {
	if queueIdleTimeout <= 0 {
		queueIdleTimeout = defaultQueueIdleTimeout
	}
	return &workflowDispatcher{
		queues:           map[string]*pathQueue{},
		queueIdleTimeout: queueIdleTimeout,
	}
}

// getOrCreate returns the pathQueue for path, allocating + spawning a
// drainer the first time the path is seen. The bool return value is true
// when the queue was newly allocated (caller may want to log).
func (d *workflowDispatcher) getOrCreate(path string) (*pathQueue, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if q, ok := d.queues[path]; ok {
		return q, false
	}
	q := &pathQueue{
		ch:            make(chan *mutateJob, pathQueueCap),
		drainerExited: make(chan struct{}),
	}
	q.lastUsed.Store(time.Now().UnixNano())
	d.queues[path] = q
	d.activeDrainers.Add(1)
	go d.drain(path, q)
	return q, true
}

// enqueue pushes job onto path's queue. Returns errQueueFull if the channel
// is at capacity. The caller is responsible for waiting on `job.done` when
// the request requires synchronous semantics.
//
// The depth-ahead is captured BEFORE the send so the audit line reflects
// the queue position the job actually occupies (after-the-fact len(ch) is
// always >=0 but doesn't tell us where this job landed).
func (d *workflowDispatcher) enqueue(job *mutateJob) (depthAhead int, err error) {
	q, _ := d.getOrCreate(job.path)
	// Pre-send depth tells the caller "N jobs were in front of me".
	depthAhead = len(q.ch)
	if job.awaitDurability {
		q.refcount.Add(1)
	}
	select {
	case q.ch <- job:
		q.lastUsed.Store(time.Now().UnixNano())
		return depthAhead, nil
	default:
		if job.awaitDurability {
			q.refcount.Add(-1)
		}
		return depthAhead, errQueueFull
	}
}

// drain is the per-path drainer goroutine. It pulls jobs FIFO, runs each
// under a fresh advisory lock, and self-collects on idle.
//
// Lifecycle:
//   - Block on either ch (job ready) or time.After(queueIdleTimeout)
//     (idle deadline).
//   - On job: acquire lock → ApplyAndPersist → release lock → close
//     job.done. lastUsed is bumped both on enqueue and drain.
//   - On idle: take dispatcher.mu, double-check len(ch)==0 && refcount==0,
//     delete from queues, exit. The double-check prevents a race where a
//     job arrives between our timer firing and the mu acquire (the timer
//     wins → exit; new enqueue races with delete → would lose job). With
//     the double-check, if an enqueue races in we observe len(ch)>0 (or
//     refcount>0 for sync waiters that already incremented) and stay alive.
func (d *workflowDispatcher) drain(path string, q *pathQueue) {
	defer func() {
		d.activeDrainers.Add(-1)
		close(q.drainerExited)
	}()
	for {
		select {
		case job := <-q.ch:
			d.processJob(job)
			q.lastUsed.Store(time.Now().UnixNano())
			if job.awaitDurability {
				q.refcount.Add(-1)
			}
		case <-time.After(d.queueIdleTimeout):
			// Idle deadline. Double-check under mu before dropping the queue.
			d.mu.Lock()
			if len(q.ch) > 0 || q.refcount.Load() > 0 {
				d.mu.Unlock()
				continue
			}
			delete(d.queues, path)
			d.mu.Unlock()
			return
		}
	}
}

// processJob acquires the advisory lock, runs the verb's mutator, releases
// the lock, and signals completion via job.done. All errors are reported via
// job.err — drain never propagates errors out of the goroutine.
func (d *workflowDispatcher) processJob(job *mutateJob) {
	defer close(job.done)

	lockStart := time.Now()
	timeout := job.lockTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	// stderr: io.Discard. The daemon's drainer never logs to a user-visible
	// stderr; the audit line is emitted by the caller (the JSON-RPC handler
	// or the CLI fallback). Stale-lock warnings during contention go nowhere.
	// Acceptable: the lock contract already enforces stale-PID recovery.
	lock, err := wf.NewLock(job.path, timeout, &silentWriter{})
	if err != nil {
		job.err = fmt.Errorf("lock new: %w", err)
		return
	}
	if err := lock.Acquire(); err != nil {
		job.err = err
		job.lockHeld = time.Since(lockStart)
		return
	}
	job.lockHeld = time.Since(lockStart)
	defer func() { _ = lock.Release() }()

	res, err := wf.ApplyAndPersist(job.path, job.verb, job.args, job.awaitDurability)
	job.result = res
	job.err = err
	job.completed = time.Now()
}

// silentWriter is an io.Writer that drops all writes. Used by the drainer's
// lock so stale-lock warnings don't pollute the daemon's stdout/stderr (the
// audit line is the canonical observability artifact).
type silentWriter struct{}

func (silentWriter) Write(p []byte) (int, error) { return len(p), nil }

// (The dispatcher has no explicit stop method: drainers self-collect on
// their queueIdleTimeout, and Server.Stop closes the listener so no new
// enqueues can race in. In-flight drainers finish their current job
// naturally and then exit on the next idle tick.)
