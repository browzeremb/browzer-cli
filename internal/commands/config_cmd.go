package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/browzeremb/browzer-cli/internal/config"
	"github.com/spf13/cobra"
)

func registerConfig(parent *cobra.Command) {
	parent.AddCommand(newConfigCommand(config.ConfigPath))
}

// configKeyType tags each known key with its expected value shape so
// `config set` can reject garbage before persisting it.
type configKeyType int

const (
	configTypeBool configKeyType = iota // on|off|true|false|1|0|yes|no
	configTypeInt                       // non-negative integer
	configTypeString                    // freeform (paths, etc.)
)

// knownConfigKeys maps each recognised key to its expected type.
// Used to reject typos AND reject values that can't possibly be
// meaningful (e.g. `config set tracking banana`).
var knownConfigKeys = map[string]configKeyType{
	"tracking":                    configTypeBool,
	"hook":                        configTypeBool,
	"telemetry":                   configTypeBool,
	"daemon.idle_timeout_seconds": configTypeInt,
	"daemon.socket_path":          configTypeString,
}

func knownConfigKeyNames() []string {
	out := make([]string, 0, len(knownConfigKeys))
	for k := range knownConfigKeys {
		out = append(out, k)
	}
	return out
}

func isKnownConfigKey(k string) bool {
	_, ok := knownConfigKeys[k]
	return ok
}

// validateConfigValue returns a descriptive error when the value does
// not match the key's declared type. Returns nil on success.
func validateConfigValue(key, value string) error {
	t, ok := knownConfigKeys[key]
	if !ok {
		return fmt.Errorf("unknown config key %q. Known: %v (or run `browzer config` to see current values)", key, knownConfigKeyNames())
	}
	switch t {
	case configTypeBool:
		switch strings.ToLower(value) {
		case "on", "off", "true", "false", "1", "0", "yes", "no":
			return nil
		}
		return fmt.Errorf("invalid value %q for %s: expected on|off (or true|false, 1|0, yes|no)", value, key)
	case configTypeInt:
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return fmt.Errorf("invalid value %q for %s: expected a non-negative integer", value, key)
		}
		return nil
	case configTypeString:
		return nil
	}
	return nil
}

func newConfigCommand(pathFn func() string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config [get|set] <key> [value]",
		Short: "Get or set a Browzer CLI config value",
		Long: `Persists user config to ~/.browzer/config.json.

Known keys: tracking, hook, telemetry, daemon.idle_timeout_seconds, daemon.socket_path.

Examples:
  browzer config                       # prints all
  browzer config get hook              # prints hook's value
  browzer config set hook off          # sets hook to off
  browzer config hook off              # legacy shorthand for 'set'
`,
		Args: cobra.MaximumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			cur, err := loadConfig(pathFn())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			// Normalize explicit get/set verbs into their bare forms.
			if len(args) >= 1 && (args[0] == "get" || args[0] == "set") {
				verb := args[0]
				rest := args[1:]
				if verb == "get" && len(rest) == 1 {
					return printConfigKey(cmd, cur, rest[0])
				}
				if verb == "set" && len(rest) == 2 {
					return setConfigKey(cmd, pathFn(), cur, rest[0], rest[1])
				}
				usage := "config " + verb + " <key>"
				if verb == "set" {
					usage += " <value>"
				}
				return fmt.Errorf("usage: browzer %s", usage)
			}
			switch len(args) {
			case 0:
				body, _ := json.MarshalIndent(cur, "", "  ")
				_, _ = fmt.Fprintln(out, string(body))
				return nil
			case 1:
				return printConfigKey(cmd, cur, args[0])
			case 2:
				return setConfigKey(cmd, pathFn(), cur, args[0], args[1])
			}
			return nil
		},
	}
	return cmd
}

// printConfigKey writes the value of key to stdout, or returns a
// diagnostic error if the key is unknown / unset. Keeps the silent
// success on known-but-unset keys so agents can probe defaults with
// `browzer config get hook || echo default`.
func printConfigKey(cmd *cobra.Command, cur map[string]any, key string) error {
	if v, ok := cur[key]; ok {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), v)
		return nil
	}
	if !isKnownConfigKey(key) {
		return fmt.Errorf("unknown config key %q. Known: %v (or run `browzer config` to see current values)", key, knownConfigKeyNames())
	}
	// Known key but not set — print nothing and exit 0 (caller falls
	// back to default).
	return nil
}

func setConfigKey(_ *cobra.Command, path string, cur map[string]any, key, value string) error {
	if err := validateConfigValue(key, value); err != nil {
		return err
	}
	cur[key] = value
	return saveConfig(path, cur)
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
