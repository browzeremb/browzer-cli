package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/browzeremb/browzer-cli/internal/auth"
	"github.com/browzeremb/browzer-cli/internal/config"
	"github.com/browzeremb/browzer-cli/internal/daemon"
	"github.com/browzeremb/browzer-cli/internal/telemetry"
	"github.com/browzeremb/browzer-cli/internal/tracker"
	"github.com/spf13/cobra"
)

// DaemonVersion is the CLI version string forwarded to the telemetry
// sender as `cliVersion`. Set from `main.go` via `SetDaemonVersion`;
// defaults to "dev" so unit tests that never call SetDaemonVersion
// still compile and run.
var DaemonVersion = "dev"

// SetDaemonVersion is called from `cmd/browzer/main.go` at startup with
// the ldflags-injected version.
func SetDaemonVersion(v string) { DaemonVersion = v }

func registerDaemon(parent *cobra.Command) {
	d := &cobra.Command{Use: "daemon", Short: "Manage the Browzer background daemon"}
	d.AddCommand(daemonStartCmd())
	d.AddCommand(daemonStopCmd())
	d.AddCommand(daemonStatusCmd())
	parent.AddCommand(d)
}

func daemonStartCmd() *cobra.Command {
	var background bool
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the daemon (foreground unless --background)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if background {
				exe, err := os.Executable()
				if err != nil {
					return err
				}
				p := exec.Command(exe, "daemon", "start")
				p.Stdout = nil
				p.Stderr = nil
				p.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
				return p.Start()
			}
			sockPath := config.SocketPath(os.Getuid())
			srv := daemon.NewServer(daemon.Options{
				SocketPath:  sockPath,
				DBPath:      config.HistoryDBPath(),
				IdleTimeout: time.Duration(config.DefaultDaemonIdleSeconds) * time.Second,
			})
			deps, tr, err := defaultDaemonDeps()
			if err != nil {
				return err
			}
			srv.Wire(deps)
			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()
			if err := writePID(); err != nil {
				return err
			}
			defer os.Remove(config.PIDPath())

			// Telemetry flusher — best-effort. Runs only when:
			//   (a) tracker opened successfully, AND
			//   (b) the current login has `telemetryConsentAt` set on the
			//       server side (captured into the credentials file during
			//       `browzer login`).
			// When either condition is false the flusher is skipped; local
			// SQLite tracking continues so `browzer gain` keeps working.
			if tr != nil {
				if creds := auth.LoadCredentials(); creds != nil &&
					creds.TelemetryConsentAt != nil && *creds.TelemetryConsentAt != "" {
					sender := telemetry.NewSender(
						creds.Server+"/api/telemetry/usage",
						creds.AccessToken,
						DaemonVersion,
					)
					b := telemetry.NewBatcher(tr, sender.Send, telemetry.BatcherOptions{})
					go b.Run(ctx)
				}
				defer tr.Close()
			}

			fmt.Fprintln(cmd.OutOrStderr(), "browzer daemon listening on", sockPath)
			return srv.Serve(ctx)
		},
	}
	cmd.Flags().BoolVar(&background, "background", false, "fork into background")
	return cmd
}

func daemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the running daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			cli := daemon.NewClient(config.SocketPath(os.Getuid()))
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
			defer cancel()
			if err := cli.Shutdown(ctx); err != nil {
				return fmt.Errorf("shutdown: %w", err)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "ok")
			return nil
		},
	}
}

func daemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report daemon status (uptime, queue, db path)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cli := daemon.NewClient(config.SocketPath(os.Getuid()))
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Second)
			defer cancel()
			h, err := cli.Health(ctx)
			if err != nil {
				fmt.Fprintln(cmd.OutOrStdout(), "not running")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "running uptime=%ds queue=%d db=%s\n", h.UptimeSec, h.QueueLen, h.DBPath)
			return nil
		},
	}
}

func writePID() error {
	return os.WriteFile(config.PIDPath(), []byte(strconv.Itoa(os.Getpid())), 0o600)
}

// defaultDaemonDeps returns Read/Track/SessionRegister wired to the
// in-package implementations (manifest cache + filter engine + session
// cache + SQLite tracker).
//
// Returns `deps, tracker, err`. The tracker handle is surfaced so the
// caller (daemonStartCmd) can pass it to the telemetry batcher
// goroutine AND own its lifecycle (Close on Serve return).
func defaultDaemonDeps() (daemon.Deps, *tracker.Tracker, error) {
	manifests := daemon.NewManifestCache(config.ManifestCachePath)
	sessions := daemon.NewSessionCache(config.SessionCachePath)

	tr, err := tracker.Open(config.HistoryDBPath())
	if err != nil {
		// Tracking is best-effort; never let it block the daemon.
		fmt.Fprintln(os.Stderr, "warn: tracker disabled:", err)
		tr = nil
	}

	return daemon.Deps{
		Read: func(ctx context.Context, p daemon.ReadParams) (daemon.ReadResult, error) {
			return doRead(manifests, sessions, p)
		},
		Track: func(ctx context.Context, p daemon.TrackParams) (map[string]any, error) {
			if tr == nil {
				return map[string]any{"ok": true}, nil
			}
			if err := tr.Record(tracker.Event{
				TS:           p.TS,
				Source:       p.Source,
				Command:      p.Command,
				PathHash:     p.PathHash,
				InputBytes:   p.InputBytes,
				OutputBytes:  p.OutputBytes,
				SavedTokens:  p.SavedTokens,
				SavingsPct:   p.SavingsPct,
				FilterLevel:  p.FilterLevel,
				ExecMs:       p.ExecMs,
				WorkspaceID:  p.WorkspaceID,
				SessionID:    p.SessionID,
				Model:        p.Model,
				FilterFailed: p.FilterFailed,
			}); err != nil {
				return map[string]any{"ok": false, "error": err.Error()}, nil
			}
			return map[string]any{"ok": true}, nil
		},
		SessionRegister: func(ctx context.Context, p daemon.SessionRegisterParams) (daemon.SessionRegisterResult, error) {
			model, err := sessions.Register(p.SessionID, p.TranscriptPath)
			if err != nil {
				return daemon.SessionRegisterResult{}, err
			}
			return daemon.SessionRegisterResult{Model: model}, nil
		},
	}, tr, nil
}

func doRead(manifests *daemon.ManifestCache, _ *daemon.SessionCache, p daemon.ReadParams) (daemon.ReadResult, error) {
	body, err := os.ReadFile(p.Path)
	if err != nil {
		return daemon.ReadResult{}, fmt.Errorf("read %s: %w", p.Path, err)
	}

	// Resolve the per-file manifest entry when a workspaceId is supplied.
	// Without it (or when the manifest lookup misses) we pass an empty
	// entry so ApplyFilter downgrades "aggressive" → "minimal".
	mf := daemon.ManifestFile{}
	if p.WorkspaceID != nil && *p.WorkspaceID != "" {
		if rel, ok := workspaceRelativePath(*p.WorkspaceID, p.Path); ok {
			if entry, hit := manifests.FileForPath(*p.WorkspaceID, rel); hit {
				mf = entry
			}
		}
	}

	out, level := daemon.ApplyFilter(body, p.FilterLevel, mf)
	tmp, err := os.CreateTemp("", "brz-read-*")
	if err != nil {
		return daemon.ReadResult{}, err
	}
	defer tmp.Close()
	if _, err := tmp.Write(out); err != nil {
		return daemon.ReadResult{}, err
	}
	saved := (len(body) - len(out)) / 4
	if saved < 0 {
		saved = 0
	}
	return daemon.ReadResult{
		TempPath:    tmp.Name(),
		SavedTokens: saved,
		Filter:      level,
	}, nil
}

// workspaceRelativePath returns the workspace-relative form of an absolute
// path by reading the workspace's project config (`.browzer/config.json`)
// via the manifest cache path convention (the workspace root is inferred
// from the manifest's sidecar config). Falls back to reading the config
// from any parent of abs that contains `.browzer/config.json` matching
// `workspaceId`. Returns ok=false when the workspace root cannot be
// determined.
func workspaceRelativePath(workspaceID, abs string) (string, bool) {
	// Walk up abs looking for .browzer/config.json whose workspaceId
	// matches. This handles the common case where the daemon is launched
	// from the same machine as the caller.
	dir := filepath.Dir(abs)
	for i := 0; i < 32; i++ {
		cfgPath := filepath.Join(dir, ".browzer", "config.json")
		if b, err := os.ReadFile(cfgPath); err == nil {
			var cfg struct {
				WorkspaceID string `json:"workspaceId"`
			}
			if json.Unmarshal(b, &cfg) == nil && cfg.WorkspaceID == workspaceID {
				rel, err := filepath.Rel(dir, abs)
				if err != nil {
					return "", false
				}
				return filepath.ToSlash(rel), true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
	return "", false
}
