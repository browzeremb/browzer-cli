package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

// DocumentUpload is one file ready for batch upload.
type DocumentUpload struct {
	// Name is the relative path inside the repo (forward slashes).
	Name string
	// Content is the raw markdown bytes.
	Content []byte
}

// BatchUploadDocs sends up to N markdown documents in a single
// multipart request to POST /api/documents/batch.
//
// IMPORTANT: each file's relative path is sent twice — once as the
// multipart filename AND once in a parallel `paths` JSON array. The
// parallel array is the server's source of truth because Fastify
// strips path components from `part.filename` for security.
//
// Returns a discriminated union: BatchKindAsync when the server
// responds 202 with batchId, or BatchKindSync for the legacy 200 shape.
func (c *Client) BatchUploadDocs(ctx context.Context, workspaceID string, files []DocumentUpload) (*BatchUploadResult, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	if err := mw.WriteField("workspaceId", workspaceID); err != nil {
		return nil, err
	}

	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.Name
	}
	pathsJSON, err := json.Marshal(paths)
	if err != nil {
		return nil, err
	}
	if err := mw.WriteField("paths", string(pathsJSON)); err != nil {
		return nil, err
	}

	for _, f := range files {
		// Use a custom part header so we can set Content-Type explicitly.
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="files"; filename=%q`, f.Name))
		header.Set("Content-Type", "text/markdown")
		part, err := mw.CreatePart(header)
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(f.Content); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	resp, err := c.do(ctx, http.MethodPost, "api/documents/batch", nil, &buf, mw.FormDataContentType())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("batch upload failed: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	// Server may return either shape — peek at the keys to decide.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		return nil, fmt.Errorf("decode batch response: %w", err)
	}

	if _, hasBatchID := raw["batchId"]; hasBatchID {
		var ack struct {
			BatchID string         `json:"batchId"`
			Jobs    []BatchJobInfo `json:"jobs"`
		}
		if err := json.Unmarshal(bodyBytes, &ack); err != nil {
			return nil, err
		}
		return &BatchUploadResult{
			Kind:    BatchKindAsync,
			BatchID: ack.BatchID,
			Jobs:    ack.Jobs,
		}, nil
	}

	var legacy struct {
		Uploaded []UploadedDoc `json:"uploaded"`
		Failed   []FailedDoc   `json:"failed"`
	}
	if err := json.Unmarshal(bodyBytes, &legacy); err != nil {
		return nil, err
	}
	return &BatchUploadResult{
		Kind:     BatchKindSync,
		Uploaded: legacy.Uploaded,
		Failed:   legacy.Failed,
	}, nil
}

// CancelBatch calls POST /api/documents/batch/:id/cancel. Idempotent
// and best-effort — callers should not fail rollback on cancel errors.
func (c *Client) CancelBatch(ctx context.Context, batchID string) error {
	return c.postJSON(ctx, "api/documents/batch/"+batchID+"/cancel", nil, nil)
}

// DeleteDocument calls DELETE /api/documents/:id?workspaceId=:ws.
func (c *Client) DeleteDocument(ctx context.Context, documentID, workspaceID string) error {
	q := url.Values{}
	q.Set("workspaceId", workspaceID)
	return c.deleteCall(ctx, "api/documents/"+documentID, q)
}
