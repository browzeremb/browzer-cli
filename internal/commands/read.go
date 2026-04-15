package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/browzeremb/browzer-cli/internal/config"
	"github.com/browzeremb/browzer-cli/internal/daemon"
	"github.com/browzeremb/browzer-cli/internal/git"
	"github.com/spf13/cobra"
)

func registerRead(parent *cobra.Command) { parent.AddCommand(newReadCommand()) }

func newReadCommand() *cobra.Command {
	var (
		filter   string
		offset   int
		limit    int
		raw      bool
		jsonOut  bool
		sockPath string
	)
	cmd := &cobra.Command{
		Use:   "read <path>",
		Short: "Read a file with optional AST-aware filtering",
		Long: `Reads a file and applies a filter level to reduce token count.

Filter levels:
  none        passthrough
  minimal     strip comments
  aggressive  keep imports + signatures + docstrings; drop function bodies
  auto        daemon picks per heuristic (default)

Range (--offset/--limit) forces filter=none — never transforms a partial read.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			abs, err := absPath(path)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			// Range forces none.
			if offset > 0 || limit > 0 {
				return readRange(abs, offset, limit, out)
			}
			if raw {
				filter = "none"
			}

			// Honor --ultra: force aggressive when auto.
			if Ultra && filter == "auto" {
				filter = "aggressive"
			}

			// Try daemon first.
			if sockPath == "" {
				sockPath = config.SocketPath(os.Getuid())
			}

			// Resolve workspaceId from the nearest .browzer/config.json so the
			// daemon can drive `--filter=aggressive` from the manifest cache.
			var workspaceID *string
			if gitRoot := git.FindGitRoot(""); gitRoot != "" {
				if cfg, _ := config.LoadProjectConfig(gitRoot); cfg != nil && cfg.WorkspaceID != "" {
					id := cfg.WorkspaceID
					workspaceID = &id
				}
			}

			cli := daemon.NewClient(sockPath)
			ctx, cancel := context.WithTimeout(cmd.Context(), 2_000_000_000)
			defer cancel()
			res, err := cli.Read(ctx, daemon.ReadParams{
				Path:        abs,
				FilterLevel: filter,
				WorkspaceID: workspaceID,
			})
			if err == nil {
				body, rerr := os.ReadFile(res.TempPath)
				if rerr == nil {
					if jsonOut {
						return writeReadJSON(out, abs, res, body)
					}
					_, _ = out.Write(body)
					return nil
				}
			}

			// Fallback: passthrough (filter=none) if daemon is down.
			body, ferr := os.ReadFile(abs)
			if ferr != nil {
				return fmt.Errorf("read %s: %w", abs, ferr)
			}
			if jsonOut {
				return writeReadJSON(out, abs, daemon.ReadResult{Filter: "none", FilterFailed: true}, body)
			}
			_, _ = out.Write(body)
			return nil
		},
	}
	cmd.Flags().StringVar(&filter, "filter", "auto", "filter level: auto|none|minimal|aggressive")
	cmd.Flags().IntVar(&offset, "offset", 0, "1-indexed start line; forces filter=none when set")
	cmd.Flags().IntVar(&limit, "limit", 0, "max lines to return; forces filter=none when set")
	cmd.Flags().BoolVar(&raw, "raw", false, "alias for --filter=none")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON payload with filter metadata")
	cmd.Flags().StringVar(&sockPath, "daemon-socket", "", "override daemon socket path (tests)")
	return cmd
}

func readRange(path string, offset, limit int, out io.Writer) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.SplitAfter(string(body), "\n")
	if offset < 0 {
		offset = 0
	}
	start := offset
	if start > len(lines) {
		return nil
	}
	end := len(lines)
	if limit > 0 && start+limit < end {
		end = start + limit
	}
	for i := start; i < end; i++ {
		_, _ = io.WriteString(out, lines[i])
	}
	return nil
}

func writeReadJSON(out io.Writer, path string, res daemon.ReadResult, body []byte) error {
	_, err := fmt.Fprintf(out, `{"path":%q,"filter":%q,"savedTokens":%d,"filterFailed":%t,"bytes":%q}`+"\n",
		path, res.Filter, res.SavedTokens, res.FilterFailed, body)
	return err
}

func absPath(p string) (string, error) {
	if strings.HasPrefix(p, "/") {
		return p, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return wd + "/" + p, nil
}
