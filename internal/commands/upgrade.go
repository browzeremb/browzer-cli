package commands

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/browzeremb/browzer-cli/internal/api"
	cliErrors "github.com/browzeremb/browzer-cli/internal/errors"
	"github.com/browzeremb/browzer-cli/internal/output"
	"github.com/spf13/cobra"
)

// upgradeSchemaJSON is the baked-in JSON Schema 2020-12 doc describing
// the payload `browzer upgrade --json` emits. SKILLs use `--schema` to
// discover the shape without making a network call.
const upgradeSchemaJSON = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "UpgradeResponse",
  "type": "object",
  "required": ["current", "latest", "outdated", "installChannel", "upgradeCommand"],
  "properties": {
    "current":         {"type": "string"},
    "latest":          {"type": "string"},
    "outdated":        {"type": "boolean"},
    "releaseUrl":      {"type": "string"},
    "publishedAt":     {"type": "string", "format": "date-time"},
    "installChannel":  {"type": "string", "enum": ["homebrew","scoop","go","curl","unknown"]},
    "upgradeCommand":  {"type": "string"}
  }
}
`

type upgradePayload struct {
	Current        string `json:"current"`
	Latest         string `json:"latest"`
	Outdated       bool   `json:"outdated"`
	ReleaseURL     string `json:"releaseUrl,omitempty"`
	PublishedAt    string `json:"publishedAt,omitempty"`
	InstallChannel string `json:"installChannel"`
	UpgradeCommand string `json:"upgradeCommand"`
}

func registerUpgrade(parent *cobra.Command) {
	var check, schema bool

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Check for CLI upgrade",
		Long: `Check whether a newer browzer CLI release is available on GitHub and
print the install-channel-appropriate upgrade command.

` + "`--check`" + ` exits 0 when the local build matches the latest release and 10
when an upgrade is available — useful in CI/SKILL wrappers.

Use --schema to print the response JSON schema without making an API call.

Examples:
  browzer upgrade
  browzer upgrade --check
  browzer upgrade --json --save /tmp/upgrade.json
  browzer upgrade --schema`,
		RunE: func(cmd *cobra.Command, args []string) error {
			jsonFlag, _ := cmd.Flags().GetBool("json")
			saveFlag, _ := cmd.Flags().GetString("save")

			if schema {
				if saveFlag != "" {
					return os.WriteFile(saveFlag, []byte(upgradeSchemaJSON), 0o644)
				}
				fmt.Print(upgradeSchemaJSON)
				return nil
			}

			current := cmd.Root().Version
			ctx, cancel := context.WithTimeout(rootContext(cmd), 6*time.Second)
			defer cancel()

			rel, err := api.FetchLatestRelease(ctx)
			if err != nil {
				return cliErrors.Newf("check latest release: %s", err.Error())
			}

			channel, command := detectInstallChannel()
			outdated := isOutdated(current, rel.TagName)

			payload := upgradePayload{
				Current:        current,
				Latest:         rel.TagName,
				Outdated:       outdated,
				ReleaseURL:     rel.HTMLURL,
				InstallChannel: channel,
				UpgradeCommand: command,
			}
			if !rel.PublishedAt.IsZero() {
				payload.PublishedAt = rel.PublishedAt.UTC().Format(time.RFC3339)
			}

			if err := emitOrFail(payload, output.Options{JSON: jsonFlag, Save: saveFlag}, humanUpgrade(payload)); err != nil {
				return err
			}

			if check && outdated {
				return cliErrors.WithCode(
					fmt.Sprintf("CLI outdated: %s → %s", current, rel.TagName),
					cliErrors.ExitOutdated,
				)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&check, "check", false, "Exit 10 if outdated, 0 if current (no auto-install)")
	cmd.Flags().BoolVar(&schema, "schema", false, "Print the JSON schema of the upgrade response and exit")
	cmd.Flags().Bool("json", false, "emit JSON")
	cmd.Flags().String("save", "", "write JSON to <file> (implies --json)")
	parent.AddCommand(cmd)
}

// humanUpgrade formats the payload for TTY rendering. Matches the shape
// documented in the A.10 plan so SKILL prompts and screenshots align.
func humanUpgrade(p upgradePayload) string {
	var b strings.Builder
	b.WriteString("Current: ")
	b.WriteString(p.Current)
	b.WriteString("\n")
	b.WriteString("Latest:  ")
	b.WriteString(p.Latest)
	if p.PublishedAt != "" {
		if date := strings.SplitN(p.PublishedAt, "T", 2)[0]; date != "" {
			b.WriteString(" (" + date + ")")
		}
	}
	b.WriteString("\n")
	if p.ReleaseURL != "" {
		b.WriteString("Notes:   ")
		b.WriteString(p.ReleaseURL)
		b.WriteString("\n")
	}
	b.WriteString("Install channel detected: ")
	b.WriteString(p.InstallChannel)
	b.WriteString("\n")
	if p.Outdated {
		b.WriteString("Run: ")
		b.WriteString(p.UpgradeCommand)
		b.WriteString("\n")
	} else {
		b.WriteString("You are on the latest version.\n")
	}
	return b.String()
}

// detectInstallChannel inspects the running binary's absolute path to
// guess how the user installed it. Falls back to the curl one-liner so
// the user always sees a runnable command. Best-effort — a wrong guess
// is cheap because the CLI never executes it for them.
func detectInstallChannel() (channel, command string) {
	exe, err := os.Executable()
	if err != nil {
		return "unknown", "curl -fsSL https://browzeremb.com/install.sh | sh"
	}
	// Resolve symlinks so `/opt/homebrew/bin/browzer` (a shim) reports
	// homebrew even though the real target lives under Cellar.
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	switch {
	case strings.Contains(exe, string(filepath.Separator)+"Cellar"+string(filepath.Separator)),
		strings.HasPrefix(exe, "/opt/homebrew/"),
		strings.HasPrefix(exe, "/usr/local/Cellar/"),
		strings.HasPrefix(exe, "/home/linuxbrew/.linuxbrew/"):
		return "homebrew", "brew upgrade browzeremb/tap/browzer"
	case strings.Contains(exe, string(filepath.Separator)+"scoop"+string(filepath.Separator)):
		return "scoop", "scoop update browzer"
	}
	if gobin := goBin(); gobin != "" && strings.HasPrefix(exe, gobin+string(filepath.Separator)) {
		return "go", "go install github.com/browzeremb/browzer-cli/cmd/browzer@latest"
	}
	return "curl", "curl -fsSL https://browzeremb.com/install.sh | sh"
}

// goBin returns `go env GOPATH`/bin if the go toolchain is on PATH.
// Returns "" when `go` is absent — which is fine, the caller then falls
// through to the curl channel.
func goBin() string {
	out, err := exec.Command("go", "env", "GOPATH").Output()
	if err != nil {
		return ""
	}
	gopath := strings.TrimSpace(string(out))
	if gopath == "" {
		return ""
	}
	return filepath.Join(gopath, "bin")
}

// isOutdated compares the local build tag against the latest release tag.
// Both sides are normalized (leading `v` stripped, `dev` always outdated)
// and split on dots. Pre-release suffixes (`-rc1`, `+meta`) fall back to
// lexicographic comparison of the remaining string, which is good enough
// for the common case where we're just checking major.minor.patch.
func isOutdated(current, latest string) bool {
	if current == "" || current == "dev" {
		return true
	}
	cur := normalizeVersion(current)
	lat := normalizeVersion(latest)
	if cur == lat {
		return false
	}
	curParts := strings.SplitN(cur, "-", 2)
	latParts := strings.SplitN(lat, "-", 2)
	cmp := compareNumeric(curParts[0], latParts[0])
	if cmp != 0 {
		return cmp < 0
	}
	// Same numeric core: a pre-release build (foo-rc1) is older than a
	// stable build (foo). When both have suffixes fall back to strings.
	curPre := ""
	if len(curParts) == 2 {
		curPre = curParts[1]
	}
	latPre := ""
	if len(latParts) == 2 {
		latPre = latParts[1]
	}
	if curPre == "" && latPre != "" {
		return false
	}
	if curPre != "" && latPre == "" {
		return true
	}
	return curPre < latPre
}

func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "cli-")
	v = strings.TrimPrefix(v, "v")
	return v
}

// compareNumeric compares dotted numeric version cores ("1.2.3" style).
// Returns -1, 0, or 1. Missing components (e.g. "1.0" vs "1.0.0") are
// treated as 0 so shorter versions compare correctly. Falls back to
// string compare on the first genuinely non-numeric component — keeps
// the function total without importing golang.org/x/mod/semver just
// for one call site.
func compareNumeric(a, b string) int {
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	for i := 0; i < len(ap) || i < len(bp); i++ {
		ai, bi := "0", "0"
		if i < len(ap) && ap[i] != "" {
			ai = ap[i]
		}
		if i < len(bp) && bp[i] != "" {
			bi = bp[i]
		}
		an, aerr := strconv.Atoi(ai)
		bn, berr := strconv.Atoi(bi)
		if aerr != nil || berr != nil {
			if ai == bi {
				continue
			}
			if ai < bi {
				return -1
			}
			return 1
		}
		if an != bn {
			if an < bn {
				return -1
			}
			return 1
		}
	}
	return 0
}
