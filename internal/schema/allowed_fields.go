package schema

import (
	"fmt"

	"cuelang.org/go/cue"
)

// AllowedFields returns the set of top-level field names allowed by the CUE
// definition `defName` (e.g. "#TaskBrief", "#PRD", "#TasksManifest"). The
// `#` prefix is added automatically when missing.
//
// This is the runtime SSOT for sanitizing payloads — instead of hard-coding
// allowlists in the persistence layer, callers introspect the CUE schema at
// request time. Adding a field in `schemas/workflow-v1.cue` auto-propagates
// here without any Go-side change.
//
// Returns an error when the definition does not exist in the embedded
// schema; an empty (but non-nil) map when the definition exists but has no
// declared fields (closed struct with all open keys).
func AllowedFields(defName string) (map[string]bool, error) {
	if defName == "" {
		return nil, fmt.Errorf("AllowedFields: empty defName")
	}
	if defName[0] != '#' {
		defName = "#" + defName
	}
	// Ensure the schema singleton is initialised before lookup — under
	// `go test` the test process may not have called any validator path
	// yet, so root.LookupPath would return a non-existent value.
	if _, _, _, err := loadSchema(); err != nil {
		return nil, fmt.Errorf("AllowedFields: load schema: %w", err)
	}
	v, ok := lookupDefinition(defName)
	if !ok {
		return nil, fmt.Errorf("AllowedFields: definition %q not found in embedded schema", defName)
	}
	resolved := resolveStructFromDisjunction(v)
	if resolved != nil {
		v = *resolved
	}
	fields, err := v.Fields(cue.All())
	if err != nil {
		return nil, fmt.Errorf("AllowedFields: iterate %s: %w", defName, err)
	}
	out := map[string]bool{}
	for fields.Next() {
		out[fields.Selector().Unquoted()] = true
	}
	return out, nil
}

// MustAllowedFields is the panic-on-error form, useful in package-level
// initializers where a missing definition is a build/embed bug.
func MustAllowedFields(defName string) map[string]bool {
	m, err := AllowedFields(defName)
	if err != nil {
		panic(err)
	}
	return m
}
