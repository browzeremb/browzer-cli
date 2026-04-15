package commands

import (
	"encoding/json"
	"fmt"
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
		since   string
		by      string
		jsonOut bool
		save    string
		ultra   bool
	)
	cmd := &cobra.Command{
		Use:   "gain",
		Short: "Report token savings from local Browzer activity",
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
			defer tr.Close()
			rows, err := tr.QueryAggregated(since, by)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if Ultra || ultra {
				return renderGainUltra(out, rows, since)
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
	cmd.Flags().StringVar(&by, "by", "source", "groupBy: source|command|filter|model|session")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON payload")
	cmd.Flags().StringVar(&save, "save", "", "write JSON to this path")
	cmd.Flags().BoolVar(&ultra, "ultra", false, "one-line summary")
	return cmd
}

func renderGainTable(out interface{ Write(p []byte) (int, error) }, rows []tracker.AggregatedRow, by, since string) error {
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

func renderGainUltra(out interface{ Write(p []byte) (int, error) }, rows []tracker.AggregatedRow, since string) error {
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
	_, err := fmt.Fprintf(out, "%s: -%d%% (%dk saved across %d events)\n", since, pct, totalSaved/1000, totalN)
	return err
}

func writeFileImpl(path string, body []byte) error {
	return os.WriteFile(path, body, 0o644)
}
