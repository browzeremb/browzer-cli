package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/browzeremb/browzer-cli/internal/auth"
	"github.com/browzeremb/browzer-cli/internal/config"
	"github.com/browzeremb/browzer-cli/internal/daemon"
	"github.com/browzeremb/browzer-cli/internal/telemetry"
	"github.com/browzeremb/browzer-cli/internal/tracker"
	"github.com/spf13/cobra"
)

// daemonVersion holds the CLI version forwarded to the telemetry sender.
// Stored as atomic.Value so main's SetDaemonVersion and the batcher
// goroutine can race-free.
var daemonVersion atomic.Value // stores string

func init() {
	daemonVersion.Store("dev")
}

// SetDaemonVersion is called from cmd/browzer/main.go at startup.
func SetDaemonVersion(v string) { daemonVersion.Store(v) }

// DaemonVersion returns the current version string.
func DaemonVersion() string {
	v, _ := daemonVersion.Load().(string)
	if v == "" {
		return "dev"
	}
	return v
}

// consentGatedSend wraps a telemetry SendFn so that consent is re-checked on
// every flush. If the user revokes consent via the web UI mid-session, the
// updated credentials file is re-read on the next batcher tick and the flush
// is skipped — no daemon restart required. Events remain in SQLite so
// `browzer gain` keeps working.
func consentGatedSend(realSend telemetry.SendFn) telemetry.SendFn {
	return func(ctx context.Context, buckets []tracker.Bucket) error {
		creds := auth.LoadCredentials()
		if creds == nil || creds.TelemetryConsentAt == nil || *creds.TelemetryConsentAt == "" {
			// Consent revoked or never granted — skip flush.
			return nil
		}
		return realSend(ctx, buckets)
	}
}

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
			if err := checkStaleDaemon(); err != nil {
				return err
			}
			if err := writePID(); err != nil {
				return err
			}
			defer func() { _ = os.Remove(config.PIDPath()) }()

			// C3: sweep stale /tmp/brz-read-* tempfiles every 60 s.
			go func() {
				t := time.NewTicker(60 * time.Second)
				defer t.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-t.C:
						matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "brz-read-*"))
						for _, f := range matches {
							info, err := os.Stat(f)
							if err != nil {
								continue
							}
							if time.Since(info.ModTime()) > 5*time.Minute {
								_ = os.Remove(f)
							}
						}
					}
				}
			}()

			// Telemetry flusher — best-effort. Starts whenever the tracker
			// is open and credentials contain a server URL. Consent is
			// re-checked on every flush via consentGatedSend, so a user
			// who revokes via the web UI mid-session stops flushing within
			// the next tick without needing a daemon restart.
			if tr != nil {
				creds := auth.LoadCredentials()
				if creds != nil && creds.Server != "" {
					sender := telemetry.NewSender(
						creds.Server+"/api/telemetry/usage",
						creds.AccessToken,
						DaemonVersion(),
					)
					b := telemetry.NewBatcher(tr, consentGatedSend(sender.Send), telemetry.BatcherOptions{})
					go b.Run(ctx)
				}
				// Periodic retention cleanup: runs once at startup (to clear
				// stale events from a previous install) then every hour.
				go func() {
					_ = tr.Cleanup()
					tick := time.NewTicker(1 * time.Hour)
					defer tick.Stop()
					for {
						select {
						case <-ctx.Done():
							return
						case <-tick.C:
							_ = tr.Cleanup()
						}
					}
				}()
				defer func() { _ = tr.Close() }()
			}

			_, _ = fmt.Fprintln(cmd.OutOrStderr(), "browzer daemon listening on", sockPath)
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
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "ok")
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
				_, _ = fmt.Fprintln(cmd.OutOrStdout(), "not running")
				return nil
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "running uptime=%ds queue=%d db=%s\n", h.UptimeSec, h.QueueLen, h.DBPath)
			return nil
		},
	}
}

func writePID() error {
	return os.WriteFile(config.PIDPath(), []byte(strconv.Itoa(os.Getpid())), 0o600)
}

// checkStaleDaemon removes stale PID + socket from a previous daemon
// crash. Returns nil (proceed) when no stale state exists OR when the
// previous PID is verified dead. Returns a friendly error when a daemon
// under the current uid is actually running.
func checkStaleDaemon() error {
	pidBytes, err := os.ReadFile(config.PIDPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil // fresh state
	}
	if err != nil {
		return fmt.Errorf("read pid file: %w", err)
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if perr != nil || pid <= 0 {
		// garbage — treat as stale
		_ = os.Remove(config.PIDPath())
		_ = os.Remove(config.SocketPath(os.Getuid()))
		return nil
	}
	// `kill -0 pid` probes without killing. ESRCH = dead.
	proc, _ := os.FindProcess(pid)
	if proc == nil || proc.Signal(syscall.Signal(0)) != nil {
		// dead — clean up stale artifacts
		_ = os.Remove(config.PIDPath())
		_ = os.Remove(config.SocketPath(os.Getuid()))
		return nil
	}
	return fmt.Errorf("daemon already running (pid %d); run `browzer daemon stop` first", pid)
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
	// C2: Validate path to prevent traversal into sensitive directories.
	clean := filepath.Clean(p.Path)
	if !filepath.IsAbs(clean) {
		return daemon.ReadResult{}, errors.New("path_must_be_absolute")
	}
	home, _ := os.UserHomeDir()
	sensitivePrefixes := []string{
		filepath.Join(home, ".browzer"),
		filepath.Join(home, ".ssh"),
		filepath.Join(home, ".aws"),
		filepath.Join(home, ".config"),
		"/etc",
		"/root",
		"/var/log",
	}
	for _, prefix := range sensitivePrefixes {
		if strings.HasPrefix(clean, prefix+string(filepath.Separator)) || clean == prefix {
			return daemon.ReadResult{}, errors.New("path_outside_workspace")
		}
	}

	body, err := os.ReadFile(clean)
	if err != nil {
		return daemon.ReadResult{}, fmt.Errorf("read %s: %w", clean, err)
	}

	// Resolve the per-file manifest entry when a workspaceId is supplied.
	// Without it (or when the manifest lookup misses) we pass an empty
	// entry so ApplyFilter downgrades "aggressive" → "minimal".
	mf := daemon.ManifestFile{}
	if p.WorkspaceID != nil && *p.WorkspaceID != "" {
		if rel, ok := workspaceRelativePath(*p.WorkspaceID, clean); ok {
			if entry, hit := manifests.FileForPath(*p.WorkspaceID, rel); hit {
				mf = entry
			}
		}
	}

	out, level := daemon.ApplyFilter(body, p.FilterLevel, clean, mf)
	tmp, err := os.CreateTemp("", "brz-read-*")
	if err != nil {
		return daemon.ReadResult{}, err
	}
	defer func() { _ = tmp.Close() }()
	if _, err := tmp.Write(out); err != nil {
		_ = os.Remove(tmp.Name()) // cleanup on partial write
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
