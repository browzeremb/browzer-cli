package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/browzeremb/browzer-cli/internal/api"
	"github.com/browzeremb/browzer-cli/internal/config"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/browzeremb/browzer-cli/internal/ui"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
)

// registerInit wires `browzer init`.
//
// Post-redesign behavior (see packages/cli/CLAUDE.md + the Sub-fase
// "CLI split" commit series): init is a pure bootstrap. It creates
// the workspace on the server and writes .browzer/config.json —
// nothing else. It does NOT walk the repo, does NOT parse the code
// graph, does NOT upload documents. Those are now the responsibility
// of `browzer workspace index` and `browzer workspace docs`.
//
// The old `--force` flag is gone. The only cases it served were
// "re-link this directory to a different workspace" (now
// `browzer workspace relink`) and "disconnect this directory while
// keeping the server workspace" (now `browzer workspace unlink`).
// Mixing those semantics into a single `--force` was surprising,
// especially around plan-slot accounting: users frequently thought
// --force would free the slot, when in practice it silently held on
// to the old workspace on the server.
func registerInit(parent *cobra.Command) {
	var nameFlag string
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create workspace for current git repo",
		Long: `Create a Browzer workspace for the current git repository.

init is a pure bootstrap. It creates the workspace on the server and
writes .browzer/config.json in the current repo — nothing else. No
walk, no code parse, no document upload.

After init, the typical flow is:

  browzer workspace index   # parse the code structure (folders/files/symbols)
  browzer workspace docs    # interactively pick documents to embed

If .browzer/config.json already exists, init refuses to overwrite it.
Use one of:

  browzer workspace unlink              # disconnect this directory (keeps the server workspace)
  browzer workspace delete <id>         # delete the workspace on the server (frees your plan slot)
  browzer workspace relink <id>         # point this directory at a different existing workspace

Examples:
  browzer init --name my-repo
  browzer init --dry-run --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonFlag, _ := cmd.Flags().GetBool("json")
			saveFlag, _ := cmd.Flags().GetString("save")

			gitRoot, err := requireGitRoot()
			if err != nil {
				return cliErrors.New("Not inside a git repository. Run `git init` first or change directory.")
			}

			if dryRun {
				return runInitDryRun(gitRoot, nameFlag, jsonFlag, saveFlag)
			}

			// Refuse to clobber an existing binding. See the long
			// description for the canonical next-step commands.
			existing, err := config.LoadProjectConfig(gitRoot)
			if err != nil {
				return err
			}
			if existing != nil {
				return cliErrors.Newf(
					"Already linked to workspace %s.\n"+
						"Options:\n"+
						"  browzer workspace unlink                 # disconnect locally (server workspace kept)\n"+
						"  browzer workspace delete %s   # delete on the server (frees your plan slot)\n"+
						"  browzer workspace relink <id>            # point this dir at a different workspace",
					existing.WorkspaceID, existing.WorkspaceID,
				)
			}

			defaultName := filepath.Base(gitRoot)
			name := resolveWorkspaceName(defaultName, nameFlag)

			ctx := rootContext(cmd)
			ac, err := requireAuth(0)
			if err != nil {
				return err
			}
			client := ac.Client

			// Idempotency guard. 2026-04-29 incident: 3 sequential
			// `browzer init` calls on the same repo (each starting
			// with .browzer/config.json missing) produced 3 separate
			// workspace nodes pointing at the same gitRoot, then the
			// duplicates burned plan slots and the user could not
			// delete them because of a separate Neo4j bug. Match
			// precedence (`reuseExistingWorkspace`):
			//
			//   1. rootPath == gitRoot          (re-init on same machine)
			//   2. name == git-remote owner/repo (same project, new path)
			//   3. name == package.json#name     (last-resort identity)
			//
			// Cross-user dedup of "same logical project" is out of
			// scope — server-side workspaces remain per-caller and
			// this guard only prevents a single caller from
			// duplicating their own work.
			var ws *api.WorkspaceDto
			if existing, lookupErr := reuseExistingWorkspace(ctx, client, gitRoot, name); lookupErr != nil {
				ui.Warn(fmt.Sprintf("Could not list existing workspaces (%v); proceeding to create.", lookupErr))
			} else if existing != nil {
				ws = existing
				ui.Success(fmt.Sprintf("Reusing existing workspace %q (%s)", existing.Name, existing.ID))
			}

			// Create the workspace. If the caller is at their plan
			// limit, the server returns 409 "workspace limit reached"
			// which bubbles up as a CliError here. We do NOT try to
			// auto-list existing workspaces on failure — that's the
			// user's call via `browzer workspace list` + `delete`.
			if ws == nil {
				sp := ui.StartSpinner("Creating workspace...")
				created, err := client.CreateWorkspace(ctx, api.CreateWorkspaceRequest{Name: name, RootPath: gitRoot})
				if err != nil {
					sp.Failure("Create workspace failed")
					return cliErrors.Newf("Failed to create workspace (%s).", err.Error())
				}
				sp.Success(fmt.Sprintf("Workspace created: %s", created.ID))
				ws = created
			}

			// Persist the config + add the cache dir to .gitignore
			// (still needed because `workspace docs` will populate the
			// SHA-256 cache on its first successful submit).
			if err := config.SaveProjectConfig(gitRoot, &config.ProjectConfig{
				Version:       config.ProjectConfigVersion,
				WorkspaceID:   ws.ID,
				WorkspaceName: ws.Name,
				Server:        ac.Credentials.Server,
			}); err != nil {
				// The workspace was already created server-side, so
				// we now have an orphan on the caller's plan. Surface
				// the id + the exact recovery command so they don't
				// have to discover it via `workspace list`.
				return cliErrors.Newf(
					"Failed to save .browzer/config.json: %v\n"+
						"Workspace %s was created on the server and is consuming 1 slot of your plan.\n"+
						"Recover with: browzer workspace delete %s",
					err, ws.ID, ws.ID,
				)
			}
			if err := config.AddCacheDirToGitignore(gitRoot); err != nil {
				ui.Warn(fmt.Sprintf("Could not update .gitignore (%v). Add \".browzer/.cache/\" manually.", err))
			}
			if err := config.InjectBrowzerSection(gitRoot); err != nil {
				ui.Warn(fmt.Sprintf("Could not update CLAUDE.md (%v). Add the Browzer KB section manually.", err))
			}

			// Best-effort plan status — never block init on this. If
			// the billing endpoint is unreachable, just skip the line.
			printPlanStatus(ctx, client)

			if jsonFlag || saveFlag != "" {
				payload := map[string]any{
					"workspaceId":   ws.ID,
					"workspaceName": ws.Name,
				}
				return emitOrFail(payload, output.Options{JSON: jsonFlag, Save: saveFlag}, "")
			}

			fmt.Println()
			ui.Success(fmt.Sprintf("Workspace %q created (%s)", ws.Name, ws.ID))
			ui.Success("Wrote .browzer/config.json")
			ui.Success("Injected Browzer KB section into CLAUDE.md")

			fmt.Println("\nNext steps:")
			fmt.Println("  browzer workspace index    # parse code structure into the workspace graph")
			fmt.Println("  browzer workspace docs     # pick which documents to embed")

			fmt.Println()
			printPluginInstructions(cmd)
			return nil
		},
	}

	cmd.Flags().StringVar(&nameFlag, "name", "", "Workspace name (default: git repo basename)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Report what would be created without calling the server")
	cmd.Flags().Bool("json", false, "emit JSON")
	cmd.Flags().String("save", "", "write JSON to <file> (implies --json)")
	parent.AddCommand(cmd)
}

// runInitDryRun prints the name + root that `init` would use without
// touching the server. Kept trivial on purpose: the old dry-run also
// walked the tree, but since init no longer walks at all, the useful
// dry-run signal collapses to "what would we create".
func runInitDryRun(gitRoot, nameFlag string, jsonFlag bool, saveFlag string) error {
	defaultName := filepath.Base(gitRoot)
	name := nameFlag
	if name == "" {
		name = defaultName
	}
	payload := map[string]any{
		"mode":          "dry-run",
		"gitRoot":       gitRoot,
		"workspaceName": name,
	}
	human := fmt.Sprintf(
		"Dry run:\n  name: %s\n  root: %s\n\n"+
			"Next: run `browzer init` (no --dry-run) to create the workspace, "+
			"then `browzer workspace index` and `browzer workspace docs` to populate it.\n",
		name, gitRoot,
	)
	return emitOrFail(payload, output.Options{JSON: jsonFlag, Save: saveFlag}, human)
}

// reuseExistingWorkspace returns a server-side workspace that should be
// adopted instead of creating a new one. Match precedence:
//
//  1. rootPath equals the local gitRoot (re-init on the same machine).
//  2. name equals the git remote's owner/repo segment (same project,
//     different local path on the same caller).
//  3. name equals the project's package.json `name` field (last-resort
//     identity for projects without a git remote, e.g. monorepo-internal
//     packages or repos hosted on hosts the regex below doesn't cover).
//
// All best-effort: any helper that fails returns the empty string and
// the loop falls through. Returns (nil, nil) when no candidate matches.
func reuseExistingWorkspace(ctx context.Context, client *api.Client, gitRoot, candidateName string) (*api.WorkspaceDto, error) {
	list, err := client.ListWorkspaces(ctx)
	if err != nil {
		return nil, err
	}
	// (1) Exact rootPath match — covers the reported bug (3x init on
	// the same machine).
	for i := range list {
		if list[i].RootPath == gitRoot {
			return &list[i], nil
		}
	}
	// (2) Git remote's owner/repo (e.g. "browzeremb/rag"). Stable across
	// machines for the same project; empty when the repo has no remote.
	if remoteName := gitRemoteOwnerRepo(gitRoot); remoteName != "" {
		for i := range list {
			if list[i].Name == remoteName {
				return &list[i], nil
			}
		}
	}
	// (3) package.json#name as final source of truth for non-git or
	// no-remote projects. Empty when the file is missing or unparseable.
	if pkgName := readPackageJSONName(gitRoot); pkgName != "" {
		for i := range list {
			if list[i].Name == pkgName {
				return &list[i], nil
			}
		}
	}
	_ = candidateName // currently informational; reserved for richer match heuristics.
	return nil, nil
}

// gitRemoteOwnerRepo returns the "owner/repo" segment derived from the
// repo's `origin` remote, or an empty string when the remote is missing
// or the URL doesn't match a recognizable host pattern. Supports both
// SSH (`git@github.com:owner/repo.git`) and HTTPS
// (`https://github.com/owner/repo.git`) forms; the trailing `.git` is
// stripped. Best-effort — exec failure or parse failure returns "".
func gitRemoteOwnerRepo(gitRoot string) string {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = gitRoot
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	url := strings.TrimSpace(string(out))
	if url == "" {
		return ""
	}
	// SSH form: git@host:owner/repo(.git)
	if idx := strings.Index(url, ":"); strings.HasPrefix(url, "git@") && idx >= 0 {
		url = url[idx+1:]
	} else {
		// HTTPS / git:// — strip scheme then host.
		for _, prefix := range []string{"https://", "http://", "git://", "ssh://"} {
			if strings.HasPrefix(url, prefix) {
				url = url[len(prefix):]
				break
			}
		}
		if slash := strings.Index(url, "/"); slash >= 0 {
			url = url[slash+1:]
		}
	}
	url = strings.TrimSuffix(url, ".git")
	url = strings.TrimSuffix(url, "/")
	// Validate shape: exactly one separator and both sides non-empty.
	parts := strings.Split(url, "/")
	if len(parts) < 2 || parts[len(parts)-1] == "" || parts[len(parts)-2] == "" {
		return ""
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1]
}

// readPackageJSONName returns the `name` field from <gitRoot>/package.json
// or the empty string when the file is missing, unreadable, malformed,
// or the field is absent/non-string. Best-effort — never errors.
func readPackageJSONName(gitRoot string) string {
	data, err := os.ReadFile(filepath.Join(gitRoot, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return ""
	}
	return strings.TrimSpace(pkg.Name)
}

// resolveWorkspaceName picks the workspace name from --name → TTY prompt
// → repo basename. Non-interactive shells silently fall back so CI can
// `browzer init` without piping input.
func resolveWorkspaceName(defaultName, flagName string) string {
	if flagName != "" {
		return flagName
	}
	if !isTTY() {
		return defaultName
	}
	value := defaultName
	err := huh.NewInput().
		Title("Workspace name:").
		Value(&value).
		Run()
	if err != nil || value == "" {
		return defaultName
	}
	return value
}
