package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/browzeremb/browzer-cli/internal/config"
	"github.com/spf13/cobra"
)

func registerConfig(parent *cobra.Command) {
	parent.AddCommand(newConfigCommand(config.ConfigPath))
}

func newConfigCommand(pathFn func() string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config <key> [value]",
		Short: "Get or set a Browzer CLI config value",
		Long: `Persists user config to ~/.browzer/config.json.

Known keys: tracking, hook, telemetry, daemon.idle_timeout_seconds, daemon.socket_path.

Examples:
  browzer config hook off
  browzer config hook
  browzer config            # prints all
`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cur, err := loadConfig(pathFn())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			switch len(args) {
			case 0:
				body, _ := json.MarshalIndent(cur, "", "  ")
				_, _ = fmt.Fprintln(out, string(body))
				return nil
			case 1:
				if v, ok := cur[args[0]]; ok {
					_, _ = fmt.Fprintln(out, v)
				}
				return nil
			case 2:
				cur[args[0]] = args[1]
				return saveConfig(pathFn(), cur)
			}
			return nil
		},
	}
	return cmd
}

func loadConfig(path string) (map[string]any, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func saveConfig(path string, m map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o600)
}
