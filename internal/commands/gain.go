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

// `Ultra` is declared in root.go as the global --ultra flag; this file
// only consumes it.

func registerGain(parent *cobra.Command) {
	parent.AddCommand(newGainCommand(config.HistoryDBPath))
}

func newGainCommand(dbPathFn func() string) *cobra.Command {
	var (
		since    string
		by       string
		jsonOut  bool
		save     string
		ultra    bool
		adoption bool
	)
	cmd := &cobra.Command{
		Use:   "gain",
		Short: "Report token savings",
		Long: `Aggregates events from ~/.browzer/history.db.

Examples:
  browzer gain                       # default: 7d, by source
  browzer gain --since 24h --by model
  browzer gain --json --save /tmp/gain.json
  browzer gain --ultra               # one-line summary
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, err := tracker.Open(dbPathFn())
			if err != nil {
				return fmt.Errorf("open tracker: %w", err)
			}
			defer func() { _ = tr.Close() }()
			out := cmd.OutOrStdout()
			if adoption {
				return runAdoptionReport(out, tr, since, jsonOut, save, Ultra || ultra)
			}
			rows, err := tr.QueryAggregated(since, by)
			if err != nil {
				return err
			}
			if Ultra || ultra {
				return renderGainUltra(out, rows, since, tr)
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
	cmd.MarkFlagsMutuallyExclusive("adoption", "by")
	return cmd
}

// classifyAdoptionSource buckets a tracker source string for the
// adoption report. Returns one of: "savedFromCli", "savedFromHooks",
// "wasted", "other". See FR-9 / FR-10.
func classifyAdoptionSource(src string) string {
	switch src {
	case "hook-cli-explore", "hook-cli-search", "hook-cli-deps", "hook-cli-ask":
		return "savedFromCli"
	case "hook-read", "hook-grep-suggested", "hook-glob-blocked", "hook-glob-suggested":
		return "savedFromHooks"
	case "cli":
		return "savedFromCli"
	case "wasted-grep", "wasted-find":
		return "wasted"
	default:
		return "other"
	}
}

// adoptionSuggestion returns a one-line replacement command for a
// wasted-* source bucket. Empty string when no suggestion applies.
func adoptionSuggestion(src string) string {
	switch src {
	case "wasted-grep", "wasted-find":
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

// runAdoptionReport renders the FR-9 / FR-10 adoption ratio: saved
// tokens (cli + hooks) vs wasted tokens (negative saved_tokens on
// wasted-* events). Wasted totals are reported as a positive magnitude.
func runAdoptionReport(
	out io.Writer,
	tr *tracker.Tracker,
	since string,
	jsonOut bool,
	save string,
	ultra bool,
) error {
	rows, err := tr.QueryAggregated(since, "source")
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
	// F-007 (AC-10): include `command` so the JSON shape parallels the
	// human-readable cell — agents can present a one-line replacement
	// without an extra lookup.
	var topWastedJSON any
	if topWasted != nil {
		topWastedJSON = map[string]any{
			"source":      topWasted.Group,
			"command":     topWasted.Group,
			"savedTokens": topWasted.SavedTokens,
			"suggestion":  adoptionSuggestion(topWasted.Group),
		}
	}
	// F-007: when wastedTotal=0, the ratio is undefined (math.Inf in
	// Go). JSON cannot encode ±Inf, and the previous string sentinel
	// "Inf" diverged from the API (null) and the human renderer ("∞").
	// Normalize on `null` for the JSON shape; the human renderer still
	// prints "∞".
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
		pct = int(totalSaved * 100 / (totalIn / 4))
	}

	// Best-effort: discover the model with the most events in the window.
	topModel := ""
	if tr != nil {
		if modelRows, err := tr.QueryAggregated(since, "model"); err == nil {
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
