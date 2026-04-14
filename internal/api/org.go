package api

import (
	"context"
	"net/url"
)

// OrgMemberDto is one member row returned by GET /api/organization/members.
type OrgMemberDto struct {
	UserID    string `json:"userId"`
	Email     string `json:"email"`
	Role      string `json:"role"`
	CreatedAt string `json:"createdAt"`
}

// OrgMembersResponse wraps the members list.
type OrgMembersResponse struct {
	Items []OrgMemberDto `json:"items"`
}

// OrgDto is the organization object returned by GET /api/organization.
type OrgDto struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Plan        string `json:"plan"`
	MemberCount int    `json:"memberCount"`
	CreatedAt   string `json:"createdAt"`
}

// OrgTreeResponse is the full response of GET /api/organization/tree.
type OrgTreeResponse struct {
	Organization OrgDto         `json:"organization"`
	Members      []OrgMemberDto `json:"members"`
}

// OrgDocDto is one document row in the org-scoped document list.
type OrgDocDto struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	WorkspaceID  string `json:"workspaceId"`
	Status       string `json:"status"`
	SizeBytes    int64  `json:"sizeBytes"`
	ChunkCount   int64  `json:"chunkCount"`
	CreatedAt    string `json:"createdAt"`
}

// OrgDocsResponse wraps the org-scoped document list.
type OrgDocsResponse struct {
	Items []OrgDocDto `json:"items"`
}

// GetOrganization calls GET /api/organization and returns the org DTO.
func (c *Client) GetOrganization(ctx context.Context) (*OrgDto, error) {
	var out OrgDto
	if err := c.getJSON(ctx, "api/organization", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListOrgMembers calls GET /api/organization/members.
func (c *Client) ListOrgMembers(ctx context.Context) (*OrgMembersResponse, error) {
	var out OrgMembersResponse
	if err := c.getJSON(ctx, "api/organization/members", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListOrgDocuments calls GET /api/documents?scope=org.
func (c *Client) ListOrgDocuments(ctx context.Context) (*OrgDocsResponse, error) {
	q := url.Values{}
	q.Set("scope", "org")
	var out OrgDocsResponse
	if err := c.getJSON(ctx, "api/documents", q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetDocument calls GET /api/documents/:id.
func (c *Client) GetDocument(ctx context.Context, id string) (*OrgDocDto, error) {
	var out OrgDocDto
	if err := c.getJSON(ctx, "api/documents/"+id, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
