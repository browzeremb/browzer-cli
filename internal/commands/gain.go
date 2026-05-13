package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"github.com/browzeremb/browzer-cli/internal/config"
	"github.com/browzeremb/browzer-cli/internal/tracker"
	"github.com/spf13/cobra"
)

// Ultra is declared in root.go as the global --ultra flag; this file
// only consumes it.

func registerGain(parent *cobra.Command) {
	parent.AddCommand(newGainCommand(config.HistoryDBPath))
}

func newGainCommand(dbPathFn func() string) *cobra.Command {
	var (
		since           string
		by              string
		jsonOut         bool
		save            string
		ultra           bool
		adoption        bool
		includeInternal bool
		hooks           bool
	)
	cmd := &cobra.Command{
		Use:   "gain",
		Short: "Report token savings",
		Long: `Aggregates events from ~/.browzer/history.db.

Examples:
  browzer gain                       # default: 7d, by source
  browzer gain --since 24h --by model
  browzer gain --json --save /tmp/gain.json
  browzer gain --hooks               # per-hook saved-tokens delta (implies --json)
  browzer gain --ultra               # one-line summary
  browzer gain --include-internal    # include workflow-audit:* events
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, err := tracker.Open(dbPathFn())
			if err != nil {
				return fmt.Errorf("open tracker: %w", err)
			}
			defer func() { _ = tr.Close() }()
			out := cmd.OutOrStdout()
			if adoption {
				return runAdoptionReport(out, tr, since, jsonOut, save, Ultra || ultra, includeInternal)
			}
			rows, err := tr.QueryAggregated(since, by, includeInternal)
			if err != nil {
				return fmt.Errorf("query aggregated (--since %q): %w", since, err)
			}
			if Ultra || ultra {
				return renderGainUltra(out, rows, since, tr)
			}
			// --hooks implies --json: emit the hooks-augmented object whenever
			// --hooks is set, regardless of whether --json or --save is also
			// present. This makes --hooks an additive flag with visible
			// behaviour: callers need not pair it with a sibling output flag.
			if hooks {
				return runHooksReport(out, tr, since, save, rows)
			}
			if jsonOut || save != "" {
				body, _ := json.MarshalIndent(rows, "", "  ")
				if save != "" {
					return writeFileImpl(save, body)
				}
				_, _ = fmt.Fprintln(out, string(body))
				return nil
			}
			return renderGainTable(out, rows, by, since)
		},
	}
	cmd.Flags().StringVar(&since, "since", "7d", "lookback window: e.g. 24h, 7d, 30d")
	cmd.Flags().StringVar(&by, "by", "source", "groupBy: source|command|filter|model|session|method")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON payload")
	cmd.Flags().StringVar(&save, "save", "", "write JSON to this path")
	cmd.Flags().BoolVar(&ultra, "ultra", false, "one-line summary")
	cmd.Flags().BoolVar(&adoption, "adoption", false, "report adoption ratio (saved vs wasted) instead of grouped totals")
	cmd.Flags().BoolVar(&includeInternal, "include-internal", false, "include workflow-audit:* events in the aggregation")
	cmd.Flags().BoolVar(&hooks, "hooks", false, "emit per-hook saved-tokens delta (implies --json output format)")
	cmd.MarkFlagsMutuallyExclusive("adoption", "by")
	// --hooks and --ultra are incompatible: --ultra suppresses all structured
	// output, so combining it with --hooks (which requires structured JSON)
	// produces a silent no-op on the hooks path.
	cmd.MarkFlagsMutuallyExclusive("hooks", "ultra")
	// --hooks and --adoption address different report shapes; combining them
	// is undefined and rejected early.
	cmd.MarkFlagsMutuallyExclusive("hooks", "adoption")
	return cmd
}

// runHooksReport queries per-hook saved-token deltas and emits a JSON payload
// that augments the standard rows with a "hooks" section. The existing rows
// (from QueryAggregated) are preserved so callers see a consistent shape
// regardless of whether the DB is empty.
func runHooksReport(out io.Writer, tr *tracker.Tracker, since, save string, rows []tracker.AggregatedRow) error {
	// Normalise nil slice to empty so JSON encodes as [] rather than null.
	if rows == nil {
		rows = []tracker.AggregatedRow{}
	}
	hookRows, err := tr.QueryHookDeltas(since)
	if err != nil {
		return fmt.Errorf("query hook deltas: %w", err)
	}
	hookMap := make(map[string]int64, len(hookRows))
	for _, r := range hookRows {
		hookMap[r.Hook] = r.SavedTokens
	}
	payload := map[string]any{
		"rows":  rows,
		"hooks": hookMap,
	}
	body, _ := json.MarshalIndent(payload, "", "  ")
	if save != "" {
		return writeFileImpl(save, body)
	}
	_, _ = fmt.Fprintln(out, string(body))
	return nil
}

// classifyAdoptionSource buckets a tracker source string for the
// adoption report. Returns one of: "savedFromCli", "savedFromHooks",
// "wasted", "other".
func classifyAdoptionSource(src string) string {
	switch src {
	case "hook-cli-explore", "hook-cli-search", "hook-cli-deps", "hook-cli-ask":
		return "savedFromCli"
	case "hook-read", "hook-grep-suggested", "hook-glob-blocked", "hook-glob-suggested":
		return "savedFromHooks"
	case "cli":
		return "savedFromCli"
	case "wasted-grep", "wasted-rg", "wasted-find":
		return "wasted"
	default:
		return "other"
	}
}

// adoptionSuggestion returns a one-line replacement command for a
// wasted-* source bucket. Empty string when no suggestion applies.
func adoptionSuggestion(src string) string {
	switch src {
	case "wasted-grep", "wasted-rg", "wasted-find":
		return "browzer explore \"<query>\" --json --save /tmp/explore.json"
	}
	return ""
}

func absInt64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// runAdoptionReport renders the adoption ratio: saved tokens (cli + hooks)
// vs wasted tokens (negative saved_tokens on wasted-* events). Wasted totals
// are reported as a positive magnitude.
func runAdoptionReport(
	out io.Writer,
	tr *tracker.Tracker,
	since string,
	jsonOut bool,
	save string,
	ultra bool,
	includeInternal bool,
) error {
	rows, err := tr.QueryAggregated(since, "source", includeInternal)
	if err != nil {
		return err
	}
	byPattern := map[string]int64{
		"savedFromCli":   0,
		"savedFromHooks": 0,
		"wasted":         0,
		"other":          0,
	}
	var savedTotal, wastedTotal int64
	var topWasted *tracker.AggregatedRow
	for i := range rows {
		r := rows[i]
		bucket := classifyAdoptionSource(r.Group)
		switch bucket {
		case "wasted":
			mag := absInt64(r.SavedTokens)
			wastedTotal += mag
			byPattern["wasted"] += mag
			if topWasted == nil || absInt64(r.SavedTokens) > absInt64(topWasted.SavedTokens) {
				rr := r
				topWasted = &rr
			}
		case "savedFromCli", "savedFromHooks":
			if r.SavedTokens > 0 {
				savedTotal += r.SavedTokens
			}
			byPattern[bucket] += r.SavedTokens
		default: // "other"
			if r.SavedTokens > 0 {
				savedTotal += r.SavedTokens
			}
			byPattern["other"] += r.SavedTokens
		}
	}

	var ratio float64
	if wastedTotal == 0 {
		ratio = math.Inf(1)
	} else {
		ratio = float64(savedTotal) / float64(wastedTotal)
	}

	// Build the JSON payload.
	// Include `command` so the JSON shape parallels the human-readable cell —
	// agents can present a one-line replacement without an extra lookup.
	var topWastedJSON any
	if topWasted != nil {
		topWastedJSON = map[string]any{
			"source":      topWasted.Group,
			"command":     topWasted.Group,
			"savedTokens": topWasted.SavedTokens,
			"suggestion":  adoptionSuggestion(topWasted.Group),
		}
	}
	// When wastedTotal=0 the ratio is undefined (math.Inf in Go). JSON cannot
	// encode ±Inf, so normalize on `null` for the JSON shape; the human
	// renderer still prints "∞".
	var adoptionField *float64
	if !math.IsInf(ratio, 1) {
		r := ratio
		adoptionField = &r
	}
	payload := map[string]any{
		"since":       since,
		"adoption":    adoptionField,
		"savedTotal":  savedTotal,
		"wastedTotal": wastedTotal,
		"topWasted":   topWastedJSON,
		"byPattern":   byPattern,
	}

	if jsonOut || save != "" {
		body, _ := json.MarshalIndent(payload, "", "  ")
		if save != "" {
			return writeFileImpl(save, body)
		}
		_, _ = fmt.Fprintln(out, string(body))
		return nil
	}

	if ultra {
		ratioStr := formatRatio(ratio)
		_, err := fmt.Fprintf(out, "%s adoption=%s saved=%d wasted=%d\n",
			since, ratioStr, savedTotal, wastedTotal)
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Adoption Report (%s)\n", since)
	fmt.Fprintln(&b, strings.Repeat("-", 60))
	fmt.Fprintf(&b, "Saved tokens (CLI):       %d\n", byPattern["savedFromCli"])
	fmt.Fprintf(&b, "Saved tokens (Hooks):     %d\n", byPattern["savedFromHooks"])
	fmt.Fprintf(&b, "Wasted tokens:            -%d\n", wastedTotal)
	fmt.Fprintf(&b, "Adoption ratio:           %s\n", formatRatio(ratio))
	if topWasted != nil {
		fmt.Fprintf(&b, "Top wasted:               %s (%d tokens)\n", topWasted.Group, topWasted.SavedTokens)
		if s := adoptionSuggestion(topWasted.Group); s != "" {
			fmt.Fprintf(&b, "Suggested replacement:    %s\n", s)
		}
	}
	_, err = out.Write([]byte(b.String()))
	return err
}

func formatRatio(r float64) string {
	if math.IsInf(r, 1) {
		return "∞"
	}
	return fmt.Sprintf("%.2f×", r)
}

func renderGainTable(out io.Writer, rows []tracker.AggregatedRow, by, since string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Token Savings Report (%s, by %s)\n", since, by)
	fmt.Fprintln(&b, strings.Repeat("-", 60))
	fmt.Fprintf(&b, "%-40s %8s %12s %10s\n", strings.ToUpper(by), "EVENTS", "INPUT", "SAVED")
	for _, r := range rows {
		g := r.Group
		if len(g) > 38 {
			g = g[:38]
		}
		fmt.Fprintf(&b, "%-40s %8d %12d %10d\n", g, r.N, r.InputBytes, r.SavedTokens)
	}
	_, err := out.Write([]byte(b.String()))
	return err
}

func renderGainUltra(out io.Writer, rows []tracker.AggregatedRow, since string, tr *tracker.Tracker) error {
	var totalIn, totalSaved int64
	var totalN int
	for _, r := range rows {
		totalIn += r.InputBytes
		totalSaved += r.SavedTokens
		totalN += r.N
	}
	pct := 0
	if totalIn > 0 {
		divisor := max(totalIn/4, 1)
		pct = int(totalSaved * 100 / divisor)
	}

	// Best-effort: discover the model with the most events in the window.
	topModel := ""
	if tr != nil {
		if modelRows, err := tr.QueryAggregated(since, "model", false); err == nil {
			for _, r := range modelRows {
				if r.Group != "" && r.Group != "<unknown>" {
					topModel = r.Group
					break // QueryAggregated returns sorted by N DESC
				}
			}
		}
	}

	if topModel != "" {
		_, err := fmt.Fprintf(out, "%s: -%d%% (%dk saved across %d events, top: %s)\n",
			since, pct, totalSaved/1000, totalN, topModel)
		return err
	}
	_, err := fmt.Fprintf(out, "%s: -%d%% (%dk saved across %d events)\n", since, pct, totalSaved/1000, totalN)
	return err
}

func writeFileImpl(path string, body []byte) error {
	return os.WriteFile(path, body, 0o644)
}
