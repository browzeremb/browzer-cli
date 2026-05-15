// Package upload coordinates the multipart batch upload to
// POST /api/documents/batch in chunks of UploadBatchSize.
//
// Mirrors the legacy src/lib/sync-pipeline.ts:
//   - hard cap MaxDocBytes per file (5 MiB)
//   - chunk into batches of UploadBatchSize (50)
//   - call BatchUploadDocs for each chunk
//   - poll status when the response is async (HTTP 202), unless --no-wait
//   - mutate the cache in place with returned documentIds
//   - per-file errors go to stderr, batch continues
package upload

import (
	"context"
	"fmt"
	"os"

	"github.com/browzeremb/browzer-cli/internal/api"
	"github.com/browzeremb/browzer-cli/internal/cache"
	"github.com/browzeremb/browzer-cli/internal/walker"
	"golang.org/x/sync/errgroup"
)

// UploadBatchSize is the number of files per multipart request.
const UploadBatchSize = 50

// MaxDocBytes is the per-file size ceiling. Larger files are skipped
// with a stderr warning.
const MaxDocBytes = 5 * 1024 * 1024

// readBatchParallelism caps the number of in-flight os.ReadFile calls
// per batch. Eight strikes the balance between SSD parallel-IO gain and
// FD pressure on macOS / Linux defaults; the batch itself is already
// capped at UploadBatchSize=50 so the absolute fd ceiling is bounded.
const readBatchParallelism = 8

// Result is the aggregate outcome of an UploadInBatches call.
type Result struct {
	UploadedCount int
	FailedCount   int
	// SkippedCount counts files the server rejected as benign — e.g.
	// DocumentDuplicateError when a re-upload's content hash already
	// exists in the workspace. These are NOT failures; the server
	// already has the indexed content. Counted separately so the
	// caller's exit-code logic can ignore them.
	SkippedCount int
	BatchIDs     []string // populated when noWait=true
	// FailedNames holds the document names (relative paths or server-side
	// names) that the poll reported as failed OR that the batch-ack
	// `failures[]` array carried with a non-benign reason. Populated
	// from BatchStatusResponse.Jobs when Kind == BatchKindAsync and
	// noWait=false. Empty when noWait=true (poll skipped) or when
	// Kind == BatchKindSync (FailedDoc.Name is already emitted to
	// stderr by the caller).
	FailedNames []string
	// SkippedNames mirrors FailedNames for the benign skip path —
	// DocumentDuplicateError is the common case. Surfaced to stderr
	// as a warning (not an error) so users see *why* the sync was a
	// no-op.
	SkippedNames []string
}

// UploadInBatches reads each file from disk, splits the list into
// chunks of UploadBatchSize, calls BatchUploadDocs per chunk, and
// (when noWait=false) polls each async batch to completion.
//
// The cache is mutated in place: every successful upload writes
// {sha256, documentId, size} into c.Files[relativePath].
//
// onBatchEnqueued is called once per async batch as soon as the server
// returns the batchId, BEFORE polling — useful for `init` rollback.
func UploadInBatches(
	ctx context.Context,
	client *api.Client,
	workspaceID *string,
	files []walker.DocFile,
	c *cache.DocsCache,
	onBatchEnqueued func(batchID string),
	noWait bool,
) (Result, error) {
	res := Result{}
	if len(files) == 0 {
		return res, nil
	}
	if c.Files == nil {
		c.Files = map[string]cache.CachedDoc{}
	}

	for start := 0; start < len(files); start += UploadBatchSize {
		end := min(start+UploadBatchSize, len(files))
		chunk := files[start:end]

		// Per-index slot, written by exactly one goroutine, then merged
		// in deterministic order after the errgroup waits. Disk reads
		// run up to readBatchParallelism at a time — on an SSD this is
		// the difference between serial walltime and IO-bound throughput
		// while keeping the upload payload shape (and per-file error
		// surface) byte-identical to the previous serial path.
		type readSlot struct {
			content []byte
			skipped bool
		}
		slots := make([]readSlot, len(chunk))

		// errgroup.Group{} rather than errgroup.WithContext: the per-file
		// ReadFile goroutines below never read from a context, so the
		// derived context produced by WithContext would only leak. The
		// outer `ctx` is honoured by the surrounding BatchUploadDocs /
		// PollBatchStatus calls; an in-flight ReadFile is short-lived
		// (single syscall on an SSD-backed file) so cancellation
		// granularity at the goroutine level is not worth the
		// context-plumbing overhead.
		var g errgroup.Group
		g.SetLimit(readBatchParallelism)
		for i, f := range chunk {
			i, f := i, f
			if f.Size > MaxDocBytes {
				slots[i].skipped = true
				fmt.Fprintf(os.Stderr, "  ⚠ skipping %s: %d bytes exceeds %d limit\n", f.RelativePath, f.Size, MaxDocBytes)
				continue
			}
			g.Go(func() error {
				content, err := os.ReadFile(f.AbsolutePath)
				if err != nil {
					slots[i].skipped = true
					fmt.Fprintf(os.Stderr, "  ⚠ read %s: %v\n", f.RelativePath, err)
					return nil
				}
				slots[i].content = content
				return nil
			})
		}
		// g.Wait always returns nil because the goroutines above never
		// propagate an error (they record skips inline); ignore is
		// safe.
		_ = g.Wait()

		uploads := make([]api.DocumentUpload, 0, len(chunk))
		// Track which DocFile each upload corresponds to so we can
		// update the cache by relative path after the round-trip.
		uploadIdx := make([]int, 0, len(chunk))
		for i, s := range slots {
			if s.skipped {
				res.FailedCount++
				continue
			}
			uploads = append(uploads, api.DocumentUpload{Name: chunk[i].RelativePath, Content: s.content})
			uploadIdx = append(uploadIdx, i)
		}

		if len(uploads) == 0 {
			continue
		}

		batch, err := client.BatchUploadDocs(ctx, workspaceID, uploads)
		if err != nil {
			res.FailedCount += len(uploads)
			fmt.Fprintf(os.Stderr, "  ⚠ batch upload failed: %v\n", err)
			continue
		}

		switch batch.Kind {
		case api.BatchKindAsync:
			if onBatchEnqueued != nil {
				onBatchEnqueued(batch.BatchID)
			}
			res.BatchIDs = append(res.BatchIDs, batch.BatchID)
			// Classify per-file rejections surfaced in the 202 ack's
			// `failures[]` array (server commit 9d3575d, 2026-04-22).
			// DocumentDuplicateError is benign — the server already has
			// that content indexed; treat as skipped, not failed.
			for _, f := range batch.Failures {
				if f.Reason == "DocumentDuplicateError" {
					res.SkippedCount++
					res.SkippedNames = append(res.SkippedNames, f.Name)
				} else {
					res.FailedCount++
					res.FailedNames = append(res.FailedNames,
						fmt.Sprintf("%s: %s", f.Name, f.Reason))
				}
			}
			if noWait {
				// Caller asked for fire-and-forget — record the
				// batchId and skip polling. Cache stays untouched
				// because we don't yet know the documentIds.
				continue
			}
			// If every file in this chunk was rejected up front (jobs
			// array empty) the server created zero Document nodes with
			// this batchId — polling will 404 with "batch not found".
			// Skip the poll entirely; the failures[] classification
			// above already accounted for every file.
			if len(batch.Jobs) == 0 {
				continue
			}
			final, err := client.PollBatchStatus(ctx, batch.BatchID, api.PollBatchOptions{})
			if err != nil {
				res.FailedCount += len(uploads)
				fmt.Fprintf(os.Stderr, "  ⚠ poll batch %s: %v\n", batch.BatchID, err)
				continue
			}
			// Update cache from the async ack jobs (which carry the
			// document IDs assigned at enqueue time).
			for i, j := range batch.Jobs {
				if i >= len(uploadIdx) {
					break
				}
				f := chunk[uploadIdx[i]]
				c.Files[f.RelativePath] = cache.CachedDoc{
					SHA256:     f.SHA256,
					DocumentID: j.DocumentID,
					Size:       f.Size,
				}
			}
			// Count completion from final progress.
			res.UploadedCount += final.Progress.Completed
			res.FailedCount += final.Progress.Failed
			// Collect failed document names from the job list so the
			// caller can surface them in a per-file stderr summary.
			for _, j := range final.Jobs {
				if j.Status == "failed" || j.Error != "" {
					res.FailedNames = append(res.FailedNames, j.Name)
				}
			}
		case api.BatchKindSync:
			// Legacy inline response — uploaded carries the IDs directly.
			byName := make(map[string]string, len(batch.Uploaded))
			for _, u := range batch.Uploaded {
				byName[u.Name] = u.ID
			}
			for i, f := range chunk {
				_ = i
				id, ok := byName[f.RelativePath]
				if !ok {
					continue
				}
				c.Files[f.RelativePath] = cache.CachedDoc{
					SHA256:     f.SHA256,
					DocumentID: id,
					Size:       f.Size,
				}
				res.UploadedCount++
			}
			res.FailedCount += len(batch.Failed)
			for _, f := range batch.Failed {
				fmt.Fprintf(os.Stderr, "  ⚠ %s: %s\n", f.Name, f.Error)
			}
		}
	}
	return res, nil
}
