package commands

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/spf13/cobra"
)

// registerWorkflowQuery wires `browzer workflow query <named>` — pre-baked,
// schema-validated, audit-line-emitting cross-step aggregation queries that
// replace hand-written jq pipelines in skill bodies (WF-CLI-1, WF-MIG-1).
func registerWorkflowQuery(parent *cobra.Command) {
	var asJSON bool

	cmd := &cobra.Command{
		Use:          "query <named>",
		Short:        "Run a pre-baked cross-step aggregation against workflow.json",
		Long:         queryLongHelp(),
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			registry := wf.QueryRegistry()
			def, ok := registry[name]
			if !ok {
				known := make([]string, 0, len(registry))
				for k := range registry {
					known = append(known, k)
				}
				sort.Strings(known)
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"unknown query %q (known: %s)\n", name, strings.Join(known, ", "))
				return fmt.Errorf("unknown query %q", name)
			}

			path, err := getWorkflowPath(cmd)
			if err != nil {
				return err
			}

			start := time.Now()
			_, raw, err := loadWorkflow(path)
			if err != nil {
				return err
			}

			result, err := def.Run(raw)
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "query %s: %v\n", name, err)
				return err
			}

			elapsedMs := time.Since(start).Milliseconds()

			// Audit line — symmetric with mutator verbs (NFR-4 from
			// docs/superpowers/specs/2026-04-24-workflow-redesign-design.md §13).
			// Reads don't hold the advisory lock, so lockHeldMs=0; validatedOk
			// reflects whether load+decode succeeded. Routing under --quiet /
			// --llm / BROWZER_WORKFLOW_QUIET / BROWZER_LLM is owned by
			// emitAuditLine: stderr by default; SQLite tracker on the LLM
			// gate (SA-8); dropped on the explicit-quiet gate.
			emitAuditLine(cmd, cmd.ErrOrStderr(), wf.AuditLine{
				Verb:        "query",
				Reason:      "name=" + name,
				ElapsedMs:   elapsedMs,
				ValidatedOk: true,
			})

			// Emit JSON (always); --json is reserved as a no-op flag for
			// symmetry with get-step / get-config so callers can pass it
			// uniformly. The output is JSON whether or not --json is set.
			_ = asJSON
			b, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal query result: %w", err)
			}
			return emitReadJSON(cmd, string(b))
		},
	}

	cmd.Flags().BoolVar(&asJSON, "json", false, "explicit JSON output (default; flag retained for verb symmetry)")
	cmd.Flags().String("save", "", "write the read payload to <path> instead of stdout")
	parent.AddCommand(cmd)
}

// queryLongHelp builds the cobra long-help string from the registry so that
// `browzer workflow query --help` documents every registered query.
func queryLongHelp() string {
	registry := wf.QueryRegistry()
	names := make([]string, 0, len(registry))
	for k := range registry {
		names = append(names, k)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("Run a pre-baked cross-step aggregation against workflow.json.\n\n")
	b.WriteString("Each query is implemented in Go (no jq), validated against the v1\n")
	b.WriteString("schema shape, and emits a JSON-serialisable result on stdout. A\n")
	b.WriteString("single audit line goes to stderr in the same format mutator verbs use.\n\n")
	b.WriteString("Available queries:\n")
	for _, n := range names {
		fmt.Fprintf(&b, "  %s\n      %s\n", n, registry[n].Description)
	}
	return b.String()
}
