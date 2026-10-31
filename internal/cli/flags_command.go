package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/flags"
)

// flagJSON is one row of `splice flags --json`.
type flagJSON struct {
	Name        string `json:"name"`
	Value       bool   `json:"value"`
	Default     bool   `json:"default"`
	Source      string `json:"source"`
	Stability   string `json:"stability"`
	Scope       string `json:"scope"`
	LegacyEnv   string `json:"legacy_env"`
	Description string `json:"description"`
}

func runFlags(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	asJSON := false
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			if err := writeFlagsHelp(stdout); err != nil {
				return exitCrash
			}
			return exitSuccess
		case "--json":
			asJSON = true
		default:
			return writeExecUsageError(stderr, fmt.Sprintf("unknown flags argument %q", arg))
		}
	}

	workspaceRoot, err := resolveWorkspaceRoot("", deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	resolved, err := deps.resolveConfig(workspaceRoot, config.Overrides{})
	if err != nil {
		// A missing provider does not stop flag resolution. The flags are still
		// explained, so the listing works on a fresh install.
		if len(resolved.FlagResolutions) == 0 || !(errors.Is(err, config.ErrNoActiveProvider) || errors.Is(err, config.ErrProviderRequiresModel)) {
			return writeAppError(stderr, err.Error(), exitCrash)
		}
	}

	if asJSON {
		rows := make([]flagJSON, 0, len(resolved.FlagResolutions))
		for _, r := range resolved.FlagResolutions {
			rows = append(rows, flagJSON{
				Name:        string(r.Name),
				Value:       r.Value,
				Default:     r.Default,
				Source:      r.Source,
				Stability:   r.Definition.Stability.String(),
				Scope:       r.Definition.Scope.String(),
				LegacyEnv:   r.Definition.LegacyEnv,
				Description: r.Definition.Description,
			})
		}
		if err := writePrettyJSON(stdout, rows); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, formatFlagResolutions(resolved.FlagResolutions)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

// formatFlagResolutions renders one flag per row, with the description on the
// line below. A value that differs from its default ends in "*".
func formatFlagResolutions(resolutions []flags.Resolution) string {
	lines := []string{"Flags"}
	if len(resolutions) == 0 {
		lines = append(lines, "  (none)")
		return strings.Join(lines, "\n")
	}
	nameWidth, sourceWidth := len("NAME"), len("SOURCE")
	for _, r := range resolutions {
		nameWidth = max(nameWidth, len(r.Name))
		sourceWidth = max(sourceWidth, len(r.Source))
	}
	lines = append(lines, fmt.Sprintf("  %-*s  %-5s  %-*s  %s", nameWidth, "NAME", "VALUE", sourceWidth, "SOURCE", "STABILITY"))
	changed := false
	for _, r := range resolutions {
		value := "off"
		if r.Value {
			value = "on"
		}
		mark := ""
		if r.Value != r.Default {
			mark = "*"
			changed = true
		}
		lines = append(lines, fmt.Sprintf("  %-*s  %-5s  %-*s  %s", nameWidth, r.Name, value+mark, sourceWidth, r.Source, r.Definition.Stability))
		lines = append(lines, "      "+r.Definition.Description)
	}
	if changed {
		lines = append(lines, "  * differs from the default")
	}
	return strings.Join(lines, "\n")
}

func writeFlagsHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  splice flags [flags]

Lists every feature flag with its value, the source that set it, and its
stability. The list uses the config for the current workspace.

Flags:
      --json      Print JSON array of flags
  -h, --help      Show this help

Set a flag with the "flags" config key, SPLICE_FLAGS, or the root --flags option.
See docs/FEATURE_FLAGS.md.
`)
	return err
}
