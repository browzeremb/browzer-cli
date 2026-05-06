// Package schema — scaffold.go
//
// Phase 3.1 (2026-05-06): emits a JSON skeleton for a named step type
// with EXACTLY the required fields populated with type-correct empty
// values (`""`, `[]`, `{}`, `false`, `0`). Powers
// `browzer workflow append-step --scaffold <STEP_NAME>` and is the
// drop-in replacement for the hand-curated `payload-shape.md`-style
// references the skill plugin used to ship.
//
// Single source of truth: the embedded `workflow-v1.schema.json`
// mirror at `packages/cli/internal/schema/workflow-v1.schema.json`,
// kept byte-identical with `packages/cli/schemas/workflow-v1.schema.json`
// by the `sync-embed` Makefile target (gated by `make ci-check`).
//
// Public API:
//
//	ScaffoldStep(stepName string) ([]byte, error)
//
// Returns canonical 2-space-indented JSON (HTML escaping disabled —
// matches `marshalReadJSON` so downstream `jq | cat` pipelines see
// the raw text). The skeleton is GUARANTEED to satisfy the JSON
// Schema's `required[]` arrays at every level reachable from
// the named step type. Optional fields are omitted entirely so the
// caller can selectively patch them in.
package schema

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// embeddedJSONSchema is the OpenAPI 3.0 projection of the CUE SSOT,
// mirrored from `packages/cli/schemas/workflow-v1.schema.json` so
// `go:embed` (which forbids `..` traversal) can pull it into the
// binary. The Makefile under `packages/cli/schemas/` keeps the two
// copies in sync via the `sync-embed` target; `make ci-check` fails
// the build when they drift.
//
//go:embed workflow-v1.schema.json
var embeddedJSONSchema []byte

// stepNameToSchemaName maps the canonical step-name string (the one
// callers pass to `--scaffold`) to the OpenAPI 3.0 schema component
// that holds its full per-step shape. Mirrors the
// `#StepDefinitions` block in `workflow-v1.cue` (the registry the
// codegen reads). Adding a new step name to the SSOT requires
// adding the corresponding entry here — drift is caught by the
// scaffold tests which call `ScaffoldStep` for every entry in
// `ValidStepNames`.
var stepNameToSchemaName = map[string]string{
	"PRD":                   "PRDStep",
	"TASKS_MANIFEST":        "TasksManifestStep",
	"TASK":                  "TaskStep",
	"BRAINSTORMING":         "BrainstormingStep",
	"CODE_REVIEW":           "CodeReviewStep",
	"RECEIVING_CODE_REVIEW": "ReceivingCodeReviewStep",
	"WRITE_TESTS":           "WriteTestsStep",
	"UPDATE_DOCS":           "UpdateDocsStep",
	"FEATURE_ACCEPTANCE":    "FeatureAcceptanceStep",
	"COMMIT":                "CommitStep",
}

// scaffoldCache is the lazy-decoded schema components map. Populated on
// first call to ScaffoldStep, reused on every subsequent invocation.
var scaffoldCache struct {
	once    func() error
	schemas map[string]any
	err     error
}

// loadScaffoldSchemas decodes the embedded JSON Schema once and caches
// the `components.schemas` map by name. Subsequent calls hit the cache
// in O(1).
func loadScaffoldSchemas() (map[string]any, error) {
	if scaffoldCache.schemas != nil || scaffoldCache.err != nil {
		return scaffoldCache.schemas, scaffoldCache.err
	}
	var doc map[string]any
	if err := json.Unmarshal(embeddedJSONSchema, &doc); err != nil {
		scaffoldCache.err = fmt.Errorf("scaffold: parse embedded json schema: %w", err)
		return nil, scaffoldCache.err
	}
	components, _ := doc["components"].(map[string]any)
	if components == nil {
		scaffoldCache.err = fmt.Errorf("scaffold: embedded schema missing components")
		return nil, scaffoldCache.err
	}
	schemas, _ := components["schemas"].(map[string]any)
	if schemas == nil {
		scaffoldCache.err = fmt.Errorf("scaffold: embedded schema missing components.schemas")
		return nil, scaffoldCache.err
	}
	scaffoldCache.schemas = schemas
	return schemas, nil
}

// ScaffoldStep returns a JSON skeleton for the named step type. The
// skeleton populates every required field at every level with a
// type-correct empty value:
//
//   - `string`           → `""`
//   - `integer`/`number` → `0`
//   - `boolean`          → `false`
//   - `array`            → `[]`
//   - `object`           → recurses (or `{}` when the schema lists no
//                                     required sub-fields)
//   - `enum`             → first listed value (operator overwrites)
//   - `pattern`          → `""` (operator overwrites — the empty
//                                string fails the pattern, but the
//                                shape is what the scaffold conveys;
//                                the validator will surface the
//                                concrete pattern on retry)
//
// Optional fields are omitted entirely so callers see only the
// minimum surface they MUST fill. The wrapper StepBase fields
// (`stepId`, `name`, `status`, `applicability`, …) are included
// because they are required by every step variant via the
// OpenAPI `allOf: [StepBase, …]` pattern.
//
// stepName MUST be one of `ValidStepNames` (case-sensitive); any
// other input returns an error naming the allowlist.
//
// Output is canonical 2-space-indented JSON with HTML escaping
// disabled (matches `marshalReadJSON`). Determinism: object keys
// are emitted in alpha-sorted order so two consecutive invocations
// produce byte-identical bytes.
func ScaffoldStep(stepName string) ([]byte, error) {
	schemaName, ok := stepNameToSchemaName[stepName]
	if !ok {
		allowed := make([]string, 0, len(stepNameToSchemaName))
		for k := range stepNameToSchemaName {
			allowed = append(allowed, k)
		}
		sort.Strings(allowed)
		return nil, fmt.Errorf(
			"scaffold: unknown step type %q — allowed: %s",
			stepName,
			strings.Join(allowed, ", "),
		)
	}
	schemas, err := loadScaffoldSchemas()
	if err != nil {
		return nil, err
	}
	stepDef, ok := schemas[schemaName].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("scaffold: schema component %q not found", schemaName)
	}
	value, err := scaffoldFromComponent(stepDef, schemas, map[string]bool{})
	if err != nil {
		return nil, fmt.Errorf("scaffold %s: %w", stepName, err)
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("scaffold %s: top-level value is not an object", stepName)
	}
	// Pin the discriminator: `name` is the required string field every
	// StepDefinition declares with a single-element enum (`enum: [<NAME>]`).
	// scaffoldFromComponent already picks the first enum value, but we
	// pin it explicitly here so a future schema change to a multi-element
	// enum can't silently produce a wrong skeleton.
	obj["name"] = stepName
	return marshalScaffoldJSON(obj)
}

// scaffoldFromComponent recursively walks an OpenAPI 3.0 schema
// component and produces the minimum-required skeleton value.
//
// Reference handling: `$ref` strings of the form
// `#/components/schemas/<Name>` are resolved against `schemas`.
// Cycles are detected via the `seen` set — when a component refers
// back to itself (directly or indirectly), the second visit returns
// an empty object/array so we never infinite-loop.
//
// allOf composition: required[] arrays from every allOf branch are
// merged with the component's own required[]; properties[] entries
// from referenced components are inherited so StepBase fields
// (`stepId`, `name`, …) appear on every per-step skeleton.
func scaffoldFromComponent(component map[string]any, schemas map[string]any, seen map[string]bool) (any, error) {
	// Resolve $ref first.
	if ref, ok := component["$ref"].(string); ok {
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		if name == ref {
			return nil, fmt.Errorf("scaffold: unsupported $ref %q (only #/components/schemas/<Name> allowed)", ref)
		}
		if seen[name] {
			// Cycle: fall back to an empty struct/array. Caller
			// chooses the shape based on the parent's `type`.
			return map[string]any{}, nil
		}
		next, ok := schemas[name].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("scaffold: $ref target %q not found", name)
		}
		seen[name] = true
		defer delete(seen, name)
		return scaffoldFromComponent(next, schemas, seen)
	}

	// Merge required[] from this component AND from every allOf branch.
	required := stringSetFromAny(component["required"])
	merged := mergeProperties(component["properties"])
	if allOf, ok := component["allOf"].([]any); ok {
		for _, branch := range allOf {
			b, ok := branch.(map[string]any)
			if !ok {
				continue
			}
			// $ref branches contribute their own required + properties.
			if ref, ok := b["$ref"].(string); ok {
				name := strings.TrimPrefix(ref, "#/components/schemas/")
				if seen[name] {
					continue
				}
				target, ok := schemas[name].(map[string]any)
				if !ok {
					continue
				}
				for k, v := range stringSetFromAny(target["required"]) {
					required[k] = v
				}
				for k, v := range mergeProperties(target["properties"]) {
					if _, exists := merged[k]; !exists {
						merged[k] = v
					}
				}
				continue
			}
			// Inline branch: required[] / properties[] merge directly.
			for k, v := range stringSetFromAny(b["required"]) {
				required[k] = v
			}
			for k, v := range mergeProperties(b["properties"]) {
				if _, exists := merged[k]; !exists {
					merged[k] = v
				}
			}
		}
	}

	typ, _ := component["type"].(string)
	switch typ {
	case "object", "":
		// Default-empty when the schema declares no required fields —
		// `{}` is a valid empty object skeleton.
		out := map[string]any{}
		for fieldName := range required {
			fieldSchema, ok := merged[fieldName].(map[string]any)
			if !ok {
				// Required field with no property declaration: emit `null`
				// so the operator notices and the validator surfaces a
				// concrete diagnostic.
				out[fieldName] = nil
				continue
			}
			val, err := scaffoldFromComponent(fieldSchema, schemas, seen)
			if err != nil {
				return nil, fmt.Errorf("scaffold: field %q: %w", fieldName, err)
			}
			out[fieldName] = val
		}
		return out, nil
	case "array":
		// Always emit an empty array. The element shape is documented
		// in the schema's `items` but the scaffold does NOT auto-fill
		// element instances — that would force the operator to delete
		// fake entries before patching real ones.
		return []any{}, nil
	case "string":
		// Honor enum: pick the first declared value so the discriminator
		// strings (e.g. PRDStep.name = "PRD") land correctly. The caller
		// (ScaffoldStep) re-pins `name` defensively for the same reason.
		if enum, ok := component["enum"].([]any); ok && len(enum) > 0 {
			if first, ok := enum[0].(string); ok {
				return first, nil
			}
		}
		return "", nil
	case "integer", "number":
		return 0, nil
	case "boolean":
		return false, nil
	case "null":
		return nil, nil
	default:
		return nil, fmt.Errorf("scaffold: unsupported type %q", typ)
	}
}

// mergeProperties returns a fresh map[string]any from the OpenAPI
// `properties` value. Nil-tolerant: a missing or wrong-typed
// properties block returns an empty map rather than panicking.
func mergeProperties(raw any) map[string]any {
	out := map[string]any{}
	if raw == nil {
		return out
	}
	props, ok := raw.(map[string]any)
	if !ok {
		return out
	}
	for k, v := range props {
		out[k] = v
	}
	return out
}

// stringSetFromAny coerces an OpenAPI `required` array to a string set.
// Tolerates nil / wrong type by returning an empty set — the caller
// proceeds with whatever required[] entries the schema did declare.
func stringSetFromAny(raw any) map[string]bool {
	out := map[string]bool{}
	arr, ok := raw.([]any)
	if !ok {
		return out
	}
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out[s] = true
		}
	}
	return out
}

// marshalScaffoldJSON encodes the skeleton with 2-space indent and
// HTML escaping disabled. Matches `marshalReadJSON` (workflow_save.go)
// so save / pipe consumers see the same byte stream.
func marshalScaffoldJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("scaffold: marshal: %w", err)
	}
	// json.Encoder appends a trailing newline; keep it so the file-on-
	// disk shape matches the stdout shape.
	return buf.Bytes(), nil
}
