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
	// TelemetryConsentAt is the ISO-8601 timestamp at which the user
	// consented to CLI telemetry buckets (0017_telemetry_consent.sql),
	// or nil when they have not yet consented. Persisted into the
	// local credentials file so the daemon's telemetry batcher can
	// gate on it without an extra round-trip on every flush.
	TelemetryConsentAt *string `json:"telemetryConsentAt,omitempty"`
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

// AskRequest is the body of POST /ask.
type AskRequest struct {
	Question    string `json:"question"`
	WorkspaceID string `json:"workspaceId,omitempty"`
}

// AskSource is one cited source in the /ask response.
// After B14/B15 the server deduplicates by documentName server-side;
// Positions holds the 1-based chunk indices that came from this document.
type AskSource struct {
	Text         string  `json:"text"`
	DocumentName string  `json:"documentName"`
	Score        float64 `json:"score"`
	// Positions lists the 1-based chunk positions from this document.
	// Present on servers >= B14; nil/empty on older servers.
	Positions []int `json:"positions,omitempty"`
}

// AskTiming carries wall-clock durations (ms) for the retrieval stages.
type AskTiming struct {
	Search *int `json:"search,omitempty"`
	Graph  *int `json:"graph,omitempty"`
}

// AskResponse is the body of a successful POST /ask response.
type AskResponse struct {
	Answer     string      `json:"answer"`
	Sources    []AskSource `json:"sources"`
	CacheHit   bool        `json:"cacheHit"`
	SourceRefs []string    `json:"sourceRefs"`
	Timing     *AskTiming  `json:"timing,omitempty"`
}

// WorkspaceManifestSymbol is one symbol entry in a ManifestFile.
// Mirrors the server-side shape emitted by
// `packages/core/src/graph/get-workspace-manifest.ts`.
type WorkspaceManifestSymbol struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Signature string `json:"signature"`
	Doc       string `json:"doc"`
}

// WorkspaceManifestFile is the per-file entry keyed by workspace-relative path.
type WorkspaceManifestFile struct {
	IndexedAt string                    `json:"indexedAt"`
	Language  string                    `json:"language"`
	LineCount int                       `json:"lineCount"`
	Symbols   []WorkspaceManifestSymbol `json:"symbols"`
	Imports   []string                  `json:"imports"`
	Exports   []string                  `json:"exports"`
}

// WorkspaceManifest is the response body of
// `GET /api/workspaces/:id/manifest` — consumed by the daemon's
// manifest cache to drive `filterLevel: "aggressive"` in
// `browzer read` and the rewrite-read hook.
type WorkspaceManifest struct {
	WorkspaceID string                           `json:"workspaceId"`
	IndexedAt   string                           `json:"indexedAt"`
	Files       map[string]WorkspaceManifestFile `json:"files"`
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
