// Package schema provides helpers for printing JSON Schema 2020-12
// response shapes for CLI commands that expose a --schema flag.
//
// Each exported PrintXxxSchema function writes the schema for one
// command's response payload to stdout (or a --save file). Callers
// pass the raw JSON string via PrintOrSave; baked-in constants live
// next to the response type they describe.
package schema

import (
	"fmt"
	"os"
)

// Print writes the given JSON Schema string to stdout.
func Print(schemaJSON string) error {
	_, err := fmt.Print(schemaJSON)
	return err
}

// PrintToFile writes schemaJSON to a file path.
func PrintToFile(schemaJSON, path string) error {
	return os.WriteFile(path, []byte(schemaJSON), 0o644)
}

// PrintOrSave writes schemaJSON to stdout (save == "") or to a file.
func PrintOrSave(schemaJSON, save string) error {
	if save != "" {
		return PrintToFile(schemaJSON, save)
	}
	return Print(schemaJSON)
}

// OrgShowSchemaJSON is the JSON Schema for GET /api/organization.
const OrgShowSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "OrgShowResponse",
  "type": "object",
  "required": ["id", "name"],
  "properties": {
    "id":          {"type": "string"},
    "name":        {"type": "string"},
    "slug":        {"type": "string"},
    "plan":        {"type": "string"},
    "memberCount": {"type": "integer"},
    "createdAt":   {"type": "string"}
  }
}
`

// OrgMembersListSchemaJSON is the JSON Schema for GET /api/organization/members.
const OrgMembersListSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "OrgMembersListResponse",
  "type": "object",
  "required": ["items"],
  "properties": {
    "items": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["userId", "email", "role"],
        "properties": {
          "userId":    {"type": "string"},
          "email":     {"type": "string"},
          "role":      {"type": "string"},
          "createdAt": {"type": "string"}
        }
      }
    }
  }
}
`

// OrgDocsListSchemaJSON is the JSON Schema for GET /api/documents?scope=org.
const OrgDocsListSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "OrgDocsListResponse",
  "type": "object",
  "required": ["items"],
  "properties": {
    "items": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["id", "name"],
        "properties": {
          "id":          {"type": "string"},
          "name":        {"type": "string"},
          "workspaceId": {"type": "string"},
          "status":      {"type": "string"},
          "sizeBytes":   {"type": "integer"},
          "chunkCount":  {"type": "integer"},
          "createdAt":   {"type": "string"}
        }
      }
    }
  }
}
`

// OrgDocShowSchemaJSON is the JSON Schema for GET /api/documents/:id.
const OrgDocShowSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "OrgDocShowResponse",
  "type": "object",
  "required": ["id", "name"],
  "properties": {
    "id":          {"type": "string"},
    "name":        {"type": "string"},
    "workspaceId": {"type": "string"},
    "status":      {"type": "string"},
    "sizeBytes":   {"type": "integer"},
    "chunkCount":  {"type": "integer"},
    "createdAt":   {"type": "string"}
  }
}
`

// WorkspaceDocsListSchemaJSON is the JSON Schema for workspace docs list.
const WorkspaceDocsListSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "WorkspaceDocsListResponse",
  "type": "object",
  "required": ["workspaceId", "documents"],
  "properties": {
    "workspaceId": {"type": "string"},
    "documents": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["documentId", "relativePath"],
        "properties": {
          "documentId":   {"type": "string"},
          "relativePath": {"type": "string"},
          "sizeBytes":    {"type": "integer"},
          "chunkCount":   {"type": "integer"},
          "status":       {"type": "string"}
        }
      }
    }
  }
}
`

// WorkspaceFilesListSchemaJSON is the JSON Schema for workspace files list.
const WorkspaceFilesListSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "WorkspaceFilesListResponse",
  "type": "object",
  "required": ["workspaceId", "files"],
  "properties": {
    "workspaceId": {"type": "string"},
    "files": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["path"],
        "properties": {
          "path":        {"type": "string"},
          "language":    {"type": "string"},
          "symbolCount": {"type": "integer"},
          "lines":       {"type": "integer"}
        }
      }
    }
  }
}
`

// WorkspaceShowSchemaJSON is the JSON Schema for workspace show (full detail).
const WorkspaceShowSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "WorkspaceShowResponse",
  "type": "object",
  "required": ["id", "name"],
  "properties": {
    "id":          {"type": "string"},
    "name":        {"type": "string"},
    "rootPath":    {"type": "string"},
    "fileCount":   {"type": "integer"},
    "folderCount": {"type": "integer"},
    "symbolCount": {"type": "integer"},
    "createdAt":   {"type": "string"},
    "updatedAt":   {"type": "string"},
    "documents": {
      "type": "array",
      "items": {"type": "object"}
    },
    "files": {
      "type": "array",
      "items": {"type": "object"}
    }
  }
}
`
