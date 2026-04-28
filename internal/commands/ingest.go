package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/browzeremb/browzer-cli/internal/api"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/walker"
	"github.com/spf13/cobra"
)

func registerIngest(parent *cobra.Command) {
	var workspaceFlag string
	var serverFlag string

	cmd := &cobra.Command{
		Use:   "ingest [path]",
		Short: "Ingest local files into a Browzer workspace",
		Args:  cobra.MaximumNArgs(1),
		Long: `Ingest local markdown and text files into a Browzer workspace via the
async ingestion pipeline.

--workspace is required. Provide a workspace ID, run ` + "`browzer init`" + ` to
link the current project, or use ` + "`browzer workspace`" + ` to list available IDs.

Examples:
  browzer ingest --workspace ws_abc123
  browzer ingest docs/ --workspace ws_abc123`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Guard: workspace is required for ingest.
			if strings.TrimSpace(workspaceFlag) == "" {
				err := cliErrors.New("--workspace is required: provide a workspace ID (e.g. browzer ingest --workspace <id>)")
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), err.Error())
				return err
			}

			ac, err := requireAuth(0)
			if err != nil {
				return err
			}

			// Override the server URL when --server is provided (test / advanced use).
			if serverFlag != "" {
				ac.Client.BaseURL = strings.TrimRight(serverFlag, "/")
			}

			// Resolve the root path for the walk.
			root := "."
			if len(args) > 0 && args[0] != "" {
				root = args[0]
			}

			docs, err := walker.WalkDocs(root)
			if err != nil {
				return fmt.Errorf("ingest: walk %q: %w", root, err)
			}
			if len(docs) == 0 {
				output.Errf("no documents found under %q\n", root)
				return nil
			}

			uploads := make([]api.DocumentUpload, 0, len(docs))
			for _, d := range docs {
				content, readErr := os.ReadFile(d.AbsolutePath)
				if readErr != nil {
					output.Errf("ingest: skipping %s: %v\n", d.RelativePath, readErr)
					continue
				}
				uploads = append(uploads, api.DocumentUpload{
					Name:    d.RelativePath,
					Content: content,
				})
			}
			if len(uploads) == 0 {
				output.Errf("no readable documents found under %q\n", root)
				return nil
			}

			result, err := ac.Client.BatchUploadDocs(rootContext(cmd), &workspaceFlag, uploads)
			if err != nil {
				return fmt.Errorf("ingest: upload: %w", err)
			}

			switch result.Kind {
			case api.BatchKindAsync:
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Ingestion started — batch ID: %s (%d job(s))\n", result.BatchID, len(result.Jobs))
				if len(result.Failures) > 0 {
					output.Errf("%d file(s) rejected before upload:\n", len(result.Failures))
					for _, f := range result.Failures {
						output.Errf("  %s: %s\n", f.Name, f.Reason)
					}
				}
			default:
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Ingested %d document(s).\n", len(result.Uploaded))
				if len(result.Failed) > 0 {
					output.Errf("%d file(s) failed:\n", len(result.Failed))
					for _, f := range result.Failed {
						output.Errf("  %s: %s\n", f.Name, f.Error)
					}
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&workspaceFlag, "workspace", "", "Workspace ID (required)")
	cmd.Flags().StringVar(&serverFlag, "server", "", "Browzer server URL (overrides credentials)")
	parent.AddCommand(cmd)
}
