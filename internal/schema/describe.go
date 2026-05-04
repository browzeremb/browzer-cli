// Package schema — describe.go
//
// DescribeStepType walks the embedded CUE SSOT and emits a human-readable
// (Markdown table) or machine-readable (JSON) description of a step type's
// schema fields. Reuses the cached cue.Value from loadSchema() so the first
// call pays the ~50ms compile cost; every subsequent call is sub-millisecond.
//
// Public API:
//
//	DescribeStepType(stepName string, opts DescribeOpts) (string, error)
//	DescribeOpts struct { Field string; RequiredOnly bool; JSON bool }
package schema

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"cuelang.org/go/cue"
	"github.com/itchyny/gojq"
)

// DescribeOpts controls the output format of DescribeStepType.
type DescribeOpts struct {
	// Field is an optional jq-style path to filter the output
	// (e.g. "task.execution.scopeAdjustments[].kind"). When non-empty,
	// the function returns the matched value as JSON.
	Field string
	// RequiredOnly filters the field table to fields that have no CUE
	// default and are not nullable — i.e. the caller MUST supply them.
	RequiredOnly bool
	// JSON emits a JSON projection of the step-type schema instead of
	// a Markdown table.
	JSON bool
}

// ValidStepNames is the canonical allowlist used both for input validation
// and for the cobra --help text.
var ValidStepNames = []string{
	"BRAINSTORMING",
	"PRD",
	"TASKS_MANIFEST",
	"TASK",
	"CODE_REVIEW",
	"RECEIVING_CODE_REVIEW",
	"WRITE_TESTS",
	"UPDATE_DOCS",
	"FEATURE_ACCEPTANCE",
	"COMMIT",
	"workflow",
}

// stepPayloadDefinition maps a canonical step name to the CUE definition name
// that holds its step-specific payload (the field added on top of #StepBase).
// "workflow" is a special alias for #WorkflowV1 (the root definition).
var stepPayloadDefinition = map[string]string{
	"BRAINSTORMING":         "#Brainstorming",
	"PRD":                   "#PRD",
	"TASKS_MANIFEST":        "#TasksManifest",
	"TASK":                  "#TaskExecution",
	"CODE_REVIEW":           "#CodeReview",
	"RECEIVING_CODE_REVIEW": "#ReceivingCodeReview",
	"WRITE_TESTS":           "#WriteTests",
	"UPDATE_DOCS":           "#UpdateDocs",
	"FEATURE_ACCEPTANCE":    "#FeatureAcceptance",
	"COMMIT":                "#CommitDescriptor",
	"workflow":              "#WorkflowV1",
}

// fieldInfo is one leaf field extracted from the CUE schema.
type fieldInfo struct {
	Path        string `json:"path"`
	Required    bool   `json:"required"`
	Type        string `json:"type"`
	AddedIn     string `json:"addedIn"`
	Description string `json:"description"`
}

// DescribeStepType returns a description of the named step type. stepName must
// be one of ValidStepNames (case-sensitive). The output format is controlled
// by opts:
//
//   - Default (no flags): Markdown table sorted by field path.
//   - opts.JSON: JSON array of field objects, sorted by path.
//   - opts.Field: return the matched sub-value as JSON via gojq.
//   - opts.RequiredOnly: filter to fields with no CUE default and non-nullable.
//
// Determinism guarantee: byte-identical output across consecutive invocations
// because fields are sorted by path before emission.
func DescribeStepType(stepName string, opts DescribeOpts) (string, error) {
	if !isValidStepName(stepName) {
		allowlist := make([]string, len(ValidStepNames))
		copy(allowlist, ValidStepNames)
		sort.Strings(allowlist)
		return "", fmt.Errorf(
			"unknown step type %q — allowed: %s",
			stepName,
			strings.Join(allowlist, ", "),
		)
	}

	// Reuse the cached schema — no re-compile after first call.
	_, _, addedInMap, err := loadSchema()
	if err != nil {
		return "", fmt.Errorf("describe: load schema: %w", err)
	}

	defName, ok := stepPayloadDefinition[stepName]
	if !ok {
		return "", fmt.Errorf("describe: no definition mapped for step %q", stepName)
	}

	// F-03 (2026-05-04): look up the CUE definition through the cached
	// per-definition map (validator.lookupDefinition). Earlier this code
	// re-compiled embeddedCueSchema on every call (~50 ms), defeating
	// the sync.Once cache. Lookup now hits the map in O(1) after the
	// first describe per definition.
	defVal, ok := lookupDefinition(defName)
	if !ok {
		return "", fmt.Errorf("describe: CUE definition %s not found in schema", defName)
	}

	// Walk all leaf fields and collect fieldInfo rows.
	var fields []fieldInfo
	walkCUEFields(defVal, "", addedInMap, &fields)

	// Apply --required-only filter.
	if opts.RequiredOnly {
		fields = filterRequired(fields)
	}

	// Sort deterministically by path (invariant: byte-identical output).
	sort.Slice(fields, func(i, j int) bool {
		return fields[i].Path < fields[j].Path
	})

	// --field: apply gojq path to the JSON projection of collected fields.
	if opts.Field != "" {
		proj := fieldsToProjection(fields)
		result, err := applyGojqPath(proj, opts.Field)
		if err != nil {
			return "", fmt.Errorf("describe: --field %q: %w", opts.Field, err)
		}
		return result, nil
	}

	// --json: return JSON array (keys are sorted by json.Marshal's map
	// ordering rules — fieldInfo is a struct so ordering is field-declaration
	// order, which is stable).
	if opts.JSON {
		b, err := json.Marshal(fields)
		if err != nil {
			return "", fmt.Errorf("describe: marshal json: %w", err)
		}
		return string(b), nil
	}

	// Default: Markdown table.
	return renderDescribeMarkdown(stepName, fields), nil
}

// isValidStepName reports whether name is in ValidStepNames.
func isValidStepName(name string) bool {
	return slices.Contains(ValidStepNames, name)
}

// walkCUEFields recursively descends the CUE value and appends leaf fieldInfo
// rows to out. prefix is the dotted path built so far.
//
// Strategy: try Fields() to descend into structs; when Fields() fails or the
// struct has no further struct children, treat the value as a leaf.
//
// Special handling for `*null | #SomeDef` disjunctions: CUE's Fields()
// returns an empty iterator for disjunctions even when one branch is a
// struct. We detect this by checking whether the value's IncompleteKind()
// contains a struct-compatible kind and, if so, unify with an empty struct `{}`
// to extract the struct branch's fields.
func walkCUEFields(v cue.Value, prefix string, addedInMap map[string]string, out *[]fieldInfo) {
	fields, err := v.Fields(cue.All())
	if err != nil {
		// Leaf-level failure — if we already have a prefix this means the
		// parent called us speculatively; do nothing (parent will emit it).
		return
	}

	// Collect all field labels so we can check the count before consuming the
	// iterator (Next() is consumed — we re-call Fields() for recursion).
	type fieldEntry struct {
		label string
		child cue.Value
	}
	var entries []fieldEntry
	for fields.Next() {
		entries = append(entries, fieldEntry{label: fields.Selector().Unquoted(), child: fields.Value()})
	}

	if len(entries) == 0 {
		// No fields from this value. If it looks like it could be a
		// null|struct disjunction, try to get the struct branch via unification.
		resolved := resolveStructFromDisjunction(v)
		if resolved != nil {
			walkCUEFields(*resolved, prefix, addedInMap, out)
		}
		// If still nothing, this is a true leaf or empty struct — emit if we
		// have a prefix.
		return
	}

	for _, e := range entries {
		path := e.label
		if prefix != "" {
			path = prefix + "." + e.label
		}

		// Try to descend into the child. If it has direct sub-fields, recurse.
		subFields, subErr := e.child.Fields(cue.All())
		if subErr == nil {
			// Check if subFields has any items.
			var subEntries []fieldEntry
			for subFields.Next() {
				subEntries = append(subEntries, fieldEntry{label: subFields.Selector().Unquoted(), child: subFields.Value()})
			}
			if len(subEntries) > 0 {
				// Recurse by calling walkCUEFields on the child (it will collect
				// its own entries fresh).
				walkCUEFields(e.child, path, addedInMap, out)
				continue
			}
			// No sub-entries. Check if it's a null|struct disjunction we can resolve.
			if resolved := resolveStructFromDisjunction(e.child); resolved != nil {
				walkCUEFields(*resolved, path, addedInMap, out)
				continue
			}
		}
		// Treat as leaf.
		*out = append(*out, makeFieldInfo(path, e.child, addedInMap))
	}
}

// resolveStructFromDisjunction checks whether v is a disjunction whose
// non-null branch is a struct (e.g. `*null | #RegressionRun`). When it is,
// it returns the struct value obtained by unifying v with an empty struct.
// Returns nil when v cannot be resolved to a struct.
//
// F-02/F-03 (2026-05-04): uses v.Context() — which now returns the
// cached cuecontext from loadSchema's sync.Once because every cue.Value
// reachable from the cached schema shares the same runtime. Allocating
// a fresh cuecontext.New() here would re-introduce the cross-runtime
// contamination bug.
func resolveStructFromDisjunction(v cue.Value) *cue.Value {
	k := v.IncompleteKind()
	// Only attempt for struct-containing disjunctions.
	if k != cue.StructKind && k != (cue.NullKind|cue.StructKind) {
		return nil
	}
	ctx := v.Context()
	emptyStruct := ctx.CompileString("{}")
	unified := v.Unify(emptyStruct)
	if unified.Err() != nil {
		return nil
	}
	// Verify the unified value actually has fields.
	fields, err := unified.Fields(cue.All())
	if err != nil {
		return nil
	}
	if !fields.Next() {
		return nil
	}
	return &unified
}

// makeFieldInfo builds a fieldInfo for a leaf CUE value.
func makeFieldInfo(path string, v cue.Value, addedInMap map[string]string) fieldInfo {
	_, hasDefault := v.Default()
	required := !hasDefault && !cueValueIsNullable(v)

	return fieldInfo{
		Path:     path,
		Required: required,
		Type:     cueTypeString(v),
		AddedIn:  lookupAddedIn(addedInMap, path),
	}
}

// cueValueIsNullable returns true when the CUE value's string representation
// contains "null", meaning the schema allows null as a valid value.
func cueValueIsNullable(v cue.Value) bool {
	repr := fmt.Sprintf("%v", v)
	return strings.Contains(repr, "null")
}

// cueTypeString returns a human-readable type string for a CUE leaf value.
func cueTypeString(v cue.Value) string {
	k := v.IncompleteKind()
	switch k {
	case cue.StringKind:
		return "string"
	case cue.IntKind:
		return "int"
	case cue.FloatKind:
		return "float"
	case cue.BoolKind:
		return "bool"
	case cue.NullKind:
		return "null"
	case cue.ListKind:
		return "array"
	case cue.StructKind:
		return "object"
	case cue.BottomKind:
		return "never"
	default:
		// Disjunction or constraint — show the raw CUE string, truncated.
		s := fmt.Sprintf("%v", v)
		if len(s) > 60 {
			s = s[:57] + "..."
		}
		return s
	}
}

// filterRequired keeps only fields that have no CUE default and are not
// nullable (as determined by makeFieldInfo).
func filterRequired(fields []fieldInfo) []fieldInfo {
	out := make([]fieldInfo, 0, len(fields))
	for _, f := range fields {
		if f.Required {
			out = append(out, f)
		}
	}
	return out
}

// renderDescribeMarkdown renders a Markdown table with sorted field rows.
func renderDescribeMarkdown(stepName string, fields []fieldInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# StepType: %s\n\n", stepName)

	if len(fields) == 0 {
		b.WriteString("No fields found for this step type.\n")
		return b.String()
	}

	b.WriteString("| Field | Required | Type | AddedIn | Description |\n")
	b.WriteString("|-------|----------|------|---------|-------------|\n")

	for _, f := range fields {
		req := "no"
		if f.Required {
			req = "yes"
		}
		// Escape pipe characters in description to avoid breaking Markdown tables.
		desc := strings.ReplaceAll(f.Description, "|", "\\|")
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
			f.Path, req, f.Type, f.AddedIn, desc)
	}

	return b.String()
}

// fieldsToProjection converts a []fieldInfo to a nested map[string]any
// suitable for gojq traversal. Each field is addressable at its dotted path.
func fieldsToProjection(fields []fieldInfo) map[string]any {
	out := map[string]any{}
	for _, f := range fields {
		setNestedKey(out, f.Path, map[string]any{
			"required":    f.Required,
			"type":        f.Type,
			"addedIn":     f.AddedIn,
			"description": f.Description,
		})
	}
	return out
}

// setNestedKey sets a value at a dotted path within m, creating intermediate
// maps as needed. Array notation ([], [0]) is stripped before splitting.
func setNestedKey(m map[string]any, path string, val any) {
	// Normalize array-index notation so "foo[].bar" becomes "foo.bar".
	path = strings.ReplaceAll(path, "[]", "")
	// Strip numeric indices like [0], [1], etc.
	for strings.Contains(path, "[") {
		start := strings.Index(path, "[")
		end := strings.Index(path, "]")
		if end < start {
			break
		}
		path = path[:start] + path[end+1:]
	}
	parts := strings.Split(path, ".")
	cur := m
	for i, p := range parts {
		if p == "" {
			continue
		}
		if i == len(parts)-1 {
			cur[p] = val
			return
		}
		if _, exists := cur[p]; !exists {
			cur[p] = map[string]any{}
		}
		next, ok := cur[p].(map[string]any)
		if !ok {
			// Collision with a leaf; overwrite with a map so the path is reachable.
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
}

// applyGojqPath applies a jq path expression to the given data and returns the
// result as a compact JSON string. Uses the same gojq library as jqx.go with
// the same env-loader guard.
func applyGojqPath(data any, path string) (string, error) {
	// Ensure the expression is a valid jq expression.
	expr := path
	if !strings.HasPrefix(expr, ".") && !strings.HasPrefix(expr, "[") {
		expr = "." + expr
	}

	q, err := gojq.Parse(expr)
	if err != nil {
		return "", fmt.Errorf("jq parse %q: %w", path, err)
	}

	code, err := gojq.Compile(q,
		// Match jqx.go: block env/ENV builtins.
		gojq.WithEnvironLoader(func() []string { return nil }),
	)
	if err != nil {
		return "", fmt.Errorf("jq compile %q: %w", path, err)
	}

	iter := code.Run(data)
	v, ok := iter.Next()
	if !ok {
		return "null", nil
	}
	if e, isErr := v.(error); isErr {
		return "", fmt.Errorf("jq run %q: %w", path, e)
	}

	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("jq marshal result: %w", err)
	}
	return string(b), nil
}
