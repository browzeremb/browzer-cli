package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// GetBatchStatus calls GET /api/jobs/:batchId — one-shot, no polling.
func (c *Client) GetBatchStatus(ctx context.Context, batchID string) (*BatchStatusResponse, error) {
	var b BatchStatusResponse
	if err := c.getJSON(ctx, "api/jobs/"+batchID, nil, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// PollBatchOptions tunes the polling loop.
type PollBatchOptions struct {
	// TimeoutMs is the hard deadline. Default 30 minutes.
	TimeoutMs int
	// OnProgress is invoked with each progress update (non-304 responses).
	OnProgress func(BatchProgress)
}

// backoffSchedule mirrors the Node poller: 1s, 2s, 3s, 5s, 5s, 5s, ...
var backoffSchedule = []time.Duration{
	1 * time.Second,
	2 * time.Second,
	3 * time.Second,
	5 * time.Second,
	5 * time.Second,
}

// PollBatchStatus polls GET /api/jobs/:batchId until the batch reaches
// a terminal state (`completed` or `partial_failure`) or the deadline
// is exceeded. Sends If-None-Match on subsequent requests so the server
// can short-circuit unchanged states with 304.
func (c *Client) PollBatchStatus(ctx context.Context, batchID string, opts PollBatchOptions) (*BatchStatusResponse, error) {
	timeoutMs := opts.TimeoutMs
	if timeoutMs == 0 {
		timeoutMs = 30 * 60 * 1000 // 30 minutes
	}
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)

	var etag string
	var last *BatchStatusResponse
	attempt := 0

	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/jobs/"+batchID, nil)
		if err != nil {
			return nil, err
		}
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		req.Header.Set("Accept", "application/json")
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}

		resp, err := c.HTTP.Do(req)
		if err != nil {
			return nil, err
		}

		switch resp.StatusCode {
		case http.StatusNotModified:
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		case http.StatusOK:
			var status BatchStatusResponse
			if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
				_ = resp.Body.Close()
				return nil, err
			}
			_ = resp.Body.Close()
			last = &status
			if status.ETag != "" {
				etag = status.ETag
			}
			if opts.OnProgress != nil {
				opts.OnProgress(status.Progress)
			}
			if status.Status == "completed" || status.Status == "partial_failure" {
				return last, nil
			}
		case http.StatusNotFound:
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
			return nil, fmt.Errorf("batch %s not found", batchID)
		default:
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			_ = resp.Body.Close()
			return nil, fmt.Errorf("polling failed: HTTP %d: %s", resp.StatusCode, string(body))
		}

		delay := backoffSchedule[len(backoffSchedule)-1]
		if attempt < len(backoffSchedule) {
			delay = backoffSchedule[attempt]
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		attempt++
	}
	return nil, errors.New("polling timeout exceeded")
}
