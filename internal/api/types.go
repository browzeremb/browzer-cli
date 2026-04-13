// Package api wraps the apps/api HTTP surface used by the CLI. All
// request/response shapes mirror the legacy src/lib/api-client.ts
// definitions byte-for-byte (same JSON keys, same nullability).
package api

import "github.com/browzeremb/browzer-cli/internal/walker"

// MeResponse is the body of GET /api/auth/me.
type MeResponse struct {
	UserID           string `json:"userId"`
	Email            string `json:"email"`
	OrganizationID   string `json:"organizationId"`
	OrganizationName string `json:"organizationName"`
}

// WorkspaceDto is one row of GET /api/workspaces or GET /api/workspaces/:id.
type WorkspaceDto struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	RootPath    string `json:"rootPath"`
	FileCount   int    `json:"fileCount"`
	FolderCount int    `json:"folderCount"`
	SymbolCount int    `json:"symbolCount"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// CreateWorkspaceRequest is the body of POST /api/workspaces.
type CreateWorkspaceRequest struct {
	Name     string `json:"name"`
	RootPath string `json:"rootPath"`
}

// ParseWorkspaceRequest wraps walker.ParseTreeInput with workspaceId.
// Sent as the body of POST /api/workspaces/parse.
type ParseWorkspaceRequest struct {
	WorkspaceID string                `json:"workspaceId"`
	RootPath    string                `json:"rootPath"`
	Folders     []walker.ParsedFolder `json:"folders"`
	Files       []walker.ParsedFile   `json:"files"`
}

// SearchResult is one entry from GET /api/workspaces/:id/search.
type SearchResult struct {
	Text         string  `json:"text"`
	Position     int     `json:"position"`
	Score        float64 `json:"score"`
	DocumentName string  `json:"documentName"`
}

// SearchResponse wraps the SearchResult slice as returned by the API.
type SearchResponse struct {
	Results []SearchResult `json:"results"`
}

// ExploreEntry is one entry from GET /api/workspaces/:id/explore.
type ExploreEntry struct {
	Path       string   `json:"path"`
	Type       string   `json:"type"`
	Name       string   `json:"name"`
	LineRange  string   `json:"lineRange,omitempty"`
	Snippet    string   `json:"snippet,omitempty"`
	Score      float64  `json:"score"`
	Exports    []string `json:"exports,omitempty"`
	Imports    []string `json:"imports,omitempty"`
	ImportedBy []string `json:"importedBy,omitempty"`
	Lines      int      `json:"lines,omitempty"`
}

// ExploreResponse wraps the entries.
type ExploreResponse struct {
	Entries []ExploreEntry `json:"entries"`
}

// DepsResponse wraps the dependency graph for a single file.
type DepsResponse struct {
	Path       string   `json:"path"`
	Exports    []string `json:"exports,omitempty"`
	Imports    []string `json:"imports,omitempty"`
	ImportedBy []string `json:"importedBy,omitempty"`
}

// BatchUploadKind discriminates the two possible POST
// /api/documents/batch responses.
type BatchUploadKind string

const (
	BatchKindAsync BatchUploadKind = "async"
	BatchKindSync  BatchUploadKind = "sync"
)

// BatchJobInfo is the per-document status row inside a batch ack.
type BatchJobInfo struct {
	DocumentID string `json:"documentId"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
}

// BatchUploadResult is the discriminated union of async (HTTP 202) and
// legacy sync (HTTP 200) responses to POST /api/documents/batch.
type BatchUploadResult struct {
	Kind BatchUploadKind

	// Set when Kind == BatchKindAsync.
	BatchID string
	Jobs    []BatchJobInfo

	// Set when Kind == BatchKindSync.
	Uploaded []UploadedDoc
	Failed   []FailedDoc
}

// UploadedDoc is one entry of the legacy sync response.
type UploadedDoc struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// FailedDoc is one entry of the legacy sync failure list.
type FailedDoc struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

// BatchProgress are the counters returned by GET /api/jobs/:batchId.
type BatchProgress struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Pending   int `json:"pending"`
}

// BatchStatusResponse is the full payload of GET /api/jobs/:batchId.
type BatchStatusResponse struct {
	BatchID  string         `json:"batchId"`
	Status   string         `json:"status"` // queued | running | completed | partial_failure
	Progress BatchProgress  `json:"progress"`
	Jobs     []BatchJobInfo `json:"jobs"`
	ETag     string         `json:"etag"`
}

// DeviceCodeResponse is the body of POST /api/device/code.
type DeviceCodeResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// DeviceTokenResponse is the body of a successful POST /api/device/token.
// RefreshToken is preserved for backward compat with pre-2.B.1 fixtures.
type DeviceTokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope,omitempty"`
}
