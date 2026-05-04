package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/browzeremb/browzer-cli/internal/schema"
	wf "github.com/browzeremb/browzer-cli/internal/workflow"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func registerWorkflowAppendDispatch(parent *cobra.Command) {
	var (
		promptFile     string
		agentID        string
		renderTemplate string
		lockTimeout    time.Duration
	)

	cmd := &cobra.Command{
		Use:          "append-dispatch <stepId>",
		Short:        "Append a dispatch record to a step's dispatches[] in workflow.json",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			stepID := args[0]

			wfPath, err := getWorkflowPath(cmd)
			if err != nil {
				return err
			}

			// Resolve --prompt-file.
			if promptFile == "" {
				return fmt.Errorf("dispatch: --prompt-file is required")
			}
			promptBytes, err := os.ReadFile(promptFile)
			if err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"dispatch: prompt file not found at %s\n  hint: pass an absolute path or a path relative to the current directory\n",
					promptFile)
				return fmt.Errorf("dispatch: prompt file not found at %s", promptFile)
			}

			// Compute sha256 digest.
			sum := sha256.Sum256(promptBytes)
			digest := "sha256:" + hex.EncodeToString(sum[:])

			// promptByteCount from stat (authoritative; avoids re-reading).
			fi, err := os.Stat(promptFile)
			if err != nil {
				return fmt.Errorf("dispatch: stat prompt file: %w", err)
			}
			promptByteCount := fi.Size()

			// Resolve agent-id (default: random uuid v4).
			if agentID == "" {
				agentID = uuid.NewString()
			}

			// Resolve repo root to anchor spool path.
			repoRoot := schema.FindRepoRoot(filepath.Dir(wfPath))

			// feat-slug = basename of the directory containing workflow.json.
			featSlug := filepath.Base(filepath.Dir(wfPath))

			// Spool path: <repoRoot>/.browzer/dispatch-spool/<feat-slug>/<stepId>/<agentId>.txt
			spoolDir := filepath.Join(repoRoot, ".browzer", "dispatch-spool", featSlug, stepID)
			if err := os.MkdirAll(spoolDir, 0o755); err != nil {
				return fmt.Errorf("dispatch: mkdir spool dir: %w", err)
			}
			spoolPath := filepath.Join(spoolDir, agentID+".txt")

			// Write/overwrite spool file (idempotent: deterministic path → overwrite on repeat call).
			if err := writeSpoolFile(spoolPath, promptBytes); err != nil {
				return fmt.Errorf("dispatch: write spool file: %w", err)
			}

			// Compute promptPath relative to repo root (portable across operators).
			relPath, err := filepath.Rel(repoRoot, spoolPath)
			if err != nil {
				// Fallback: use spool path as-is.
				relPath = spoolPath
			}

			// Build DispatchRecord.
			now := time.Now().UTC().Format(time.RFC3339)
			record := map[string]any{
				"agentId":              agentID,
				"dispatchPromptDigest": digest,
				"promptByteCount":      promptByteCount,
				"promptPath":           relPath,
				"renderTemplateUsed":   renderTemplate,
				"dispatchedAt":         now,
				"model":                nil,
				"status":               "in_progress",
				"findingsAddressed":    []any{},
				"filesModified":        []any{},
				"filesCreated":         []any{},
				"filesDeleted":         []any{},
			}
			if renderTemplate == "" {
				record["renderTemplateUsed"] = nil
			}

			payloadBytes, err := json.Marshal(record)
			if err != nil {
				return fmt.Errorf("dispatch: marshal dispatch record: %w", err)
			}

			noLock, _ := cmd.Flags().GetBool("no-lock")
			if !noLock {
				noLock, _ = cmd.InheritedFlags().GetBool("no-lock")
			}

			mode, err := resolveWriteMode(cmd)
			if err != nil {
				return err
			}

			return dispatchToDaemonOrFallback(cmd, wfPath, "append-dispatch", wf.MutatorArgs{
				Args:    []string{stepID},
				Payload: payloadBytes,
			}, mode, noLock, lockTimeout)
		},
	}

	cmd.Flags().StringVar(&promptFile, "prompt-file", "", "path to prompt file (required)")
	cmd.Flags().StringVar(&agentID, "agent-id", "", "agent id (default: random uuid v4)")
	cmd.Flags().StringVar(&renderTemplate, "render-template", "", "render template name used (optional)")
	cmd.Flags().DurationVar(&lockTimeout, "lock-timeout", 5*time.Second, "advisory lock acquisition timeout")
	_ = cmd.MarkFlagRequired("prompt-file")

	parent.AddCommand(cmd)
}

// writeSpoolFile writes data to path, creating/truncating the file. This is an
// idempotent overwrite — same {featSlug, stepId, agentId} → same path, same content.
//
// F-08 (2026-05-04): the order is Write → Sync → Close. The async
// dispatch contract relies on the spool file's bytes surviving a power
// cut between the dispatch-record write (which references the spool
// path) and the subagent's read. Without fsync the truncation is
// durable but the contents may be lost, leaving a dispatch record
// pointing at a zero-byte file.
func writeSpoolFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, werr := f.Write(data); werr != nil {
		_ = f.Close()
		return werr
	}
	if syncErr := f.Sync(); syncErr != nil {
		_ = f.Close()
		return syncErr
	}
	return f.Close()
}
