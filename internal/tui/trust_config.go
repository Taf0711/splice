// trust_config.go is the GAP-I project-config trust surface (contract §15.1,
// §16, Pen frame I1): when a workspace's executable project config exists but
// the workspace is untrusted, the TUI shows WHAT that config would change
// before anything loads. The existing trust picker (newTrustPicker) owns the
// trust/decline decision; this file supplies the evidence the decision
// deserves: a typed summary of the config file's executable content.
//
// Architecture fence: these are deterministic readers of the config file.
// They never decide whether the config loads — config.Resolve and the CLI's
// trusted flag own that (an untrusted workspace skips project MCP servers,
// hooks, and plugins at the merge layer). The card projects; the runtime
// decides.
package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/hooks"
)

// projectTrustConfig is the typed "what would change" summary for one
// project config file. Every field renders on the UNTRUSTED PROJECT CONFIG
// card; zero-value fields are omitted from the render rather than shown as
// empty controls (a dead row is never advertised — frame j3ZQBu cell 4).
type projectTrustConfig struct {
	Path string // absolute path of the config file
	// MCPServerCount is the number of MCP servers the project config would
	// register. Each is an external process the project can launch.
	MCPServerCount int
	// MCPServerNames lists the server names in config order.
	MCPServerNames []string
	// HookCount is the number of EXECUTABLE hook entries: lifecycle
	// commands the project would actually run (the hooks file is enabled
	// and the entry itself is enabled).
	HookCount int
	// HookDisabledCount is the number of hook definitions the file carries
	// that would NOT run, either because the file sets "enabled": false or
	// because the entry sets "enabled": false. They are still disclosed:
	// a disabled definition is one edit away from executable.
	HookDisabledCount int
	// HookParseError is non-empty when the sibling hooks file exists but
	// the canonical hooks reader rejects it. An unreadable hooks file is
	// itself information about the workspace.
	HookParseError string
	// ProviderCount is the number of provider profiles the project defines.
	ProviderCount int
	// ActiveProviderOverride is set when the project config names its own
	// active provider.
	ActiveProviderOverride string
	// MaxTurnsOverride is set when the project config caps or raises turns.
	MaxTurnsOverride int
	// SandboxTightening lists sandbox settings the project config sets. The
	// merge layer only lets project config TIGHTEN the sandbox (never widen
	// write/read roots or open network), so every entry here is a
	// restriction the project asks for — still worth showing, since it
	// changes how commands run.
	SandboxTightening []string
	// KeybindingOverrides counts custom key bindings the project declares.
	KeybindingOverrides int
	// ParseError is non-empty when the file exists but is not valid config
	// JSON. The card shows the error honestly: an unreadable file is itself
	// information about the workspace.
	ParseError string
}

// Empty reports whether the config carries nothing worth a trust decision.
func (c projectTrustConfig) Empty() bool {
	return c.MCPServerCount == 0 && c.HookCount == 0 && c.HookDisabledCount == 0 &&
		c.HookParseError == "" && c.ProviderCount == 0 &&
		c.ActiveProviderOverride == "" && c.MaxTurnsOverride == 0 &&
		len(c.SandboxTightening) == 0 && c.KeybindingOverrides == 0 &&
		c.ParseError == ""
}

// describeProjectTrustConfig reads the project config at path and returns
// its executable-content summary. A missing file returns Empty() == true
// with no error: absence is the normal case for most workspaces. Parse
// failures are captured on the struct (not returned as errors) because an
// unparseable file is exactly what the user needs to see before deciding.
//
// The sibling hooks file is inspected independently of config.json. A
// workspace with no config.json but with a hooks.json still runs commands,
// so an absent or unparseable config.json must not hide the hooks.
func describeProjectTrustConfig(path string) projectTrustConfig {
	out := projectTrustConfig{Path: path}
	if strings.TrimSpace(path) == "" {
		return out
	}
	// hooks.json is a separate executable file next to config.json; count it
	// first so it is disclosed even when config.json is absent or broken.
	out.HookCount, out.HookDisabledCount, out.HookParseError = inspectProjectHooks(filepath.Join(filepath.Dir(path), "hooks.json"))
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out
		}
		out.ParseError = err.Error()
		return out
	}
	var cfg struct {
		ActiveProvider string `json:"activeProvider"`
		MaxTurns       int    `json:"maxTurns"`
		Providers      []struct {
			Name string `json:"name"`
		} `json:"providers"`
		MCP struct {
			Servers map[string]json.RawMessage `json:"servers"`
		} `json:"mcp"`
		Hooks   json.RawMessage `json:"hooks"`
		Sandbox struct {
			Network          string `json:"network"`
			BlockUnixSockets bool   `json:"blockUnixSockets"`
			MonitorDenials   bool   `json:"monitorDenials"`
		} `json:"sandbox"`
		KeyBindings json.RawMessage `json:"keybindings"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		out.ParseError = err.Error()
		return out
	}
	out.MCPServerCount = len(cfg.MCP.Servers)
	for name := range cfg.MCP.Servers {
		out.MCPServerNames = append(out.MCPServerNames, name)
	}
	sort.Strings(out.MCPServerNames)
	out.ProviderCount = len(cfg.Providers)
	out.ActiveProviderOverride = strings.TrimSpace(cfg.ActiveProvider)
	out.MaxTurnsOverride = cfg.MaxTurns
	if network := strings.TrimSpace(cfg.Sandbox.Network); network != "" {
		out.SandboxTightening = append(out.SandboxTightening, "network: "+network)
	}
	if cfg.Sandbox.BlockUnixSockets {
		out.SandboxTightening = append(out.SandboxTightening, "block unix sockets")
	}
	if cfg.Sandbox.MonitorDenials {
		out.SandboxTightening = append(out.SandboxTightening, "monitor sandbox denials")
	}
	out.KeybindingOverrides = countKeybindingOverrides(cfg.KeyBindings)
	return out
}

// inspectProjectHooks reads one hooks file through the canonical hooks
// reader (hooks.ConfigStore.List, a pure read of a single path) and reports
// how many entries would run, how many are defined but disabled, and the
// reader's own error when the file is unreadable.
//
// The TUI keeps no second hooks schema. The real schema is
// {"enabled": bool, "hooks": [ {..., "enabled": bool} ]}, and only the
// canonical reader is allowed to know that. A missing file is not an error:
// most workspaces have none.
func inspectProjectHooks(path string) (executable int, disabled int, parseError string) {
	if strings.TrimSpace(path) == "" {
		return 0, 0, ""
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return 0, 0, ""
		}
		return 0, 0, fmt.Errorf("read hooks file %s: %w", path, err).Error()
	}
	store, err := hooks.NewConfigStore(hooks.StoreOptions{ConfigPath: path})
	if err != nil {
		return 0, 0, fmt.Errorf("read hooks file %s: %w", path, err).Error()
	}
	// List is read-only: it parses the single file and never writes it.
	config, err := store.List()
	if err != nil {
		return 0, 0, fmt.Errorf("read hooks file %s: %w", path, err).Error()
	}
	for _, hook := range config.Hooks {
		if config.Enabled && hook.Enabled {
			executable++
			continue
		}
		disabled++
	}
	return executable, disabled, ""
}

// countHookEntries returns the number of hook commands the file at path
// would actually run. It delegates to the canonical reader through
// inspectProjectHooks; a disabled file or a disabled entry counts zero.
func countHookEntries(path string) int {
	executable, _, _ := inspectProjectHooks(path)
	return executable
}

// countKeybindingOverrides counts the top-level keys of a keybindings object.
func countKeybindingOverrides(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var asObject map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asObject); err != nil {
		return 0
	}
	return len(asObject)
}

// trustConfigCardLines renders the card body lines for the config summary.
// Format follows frame I1: the path, the change classes, the human warning,
// and the "no run starts until you choose" invariant footer.
func trustConfigCardLines(c projectTrustConfig, width int) []string {
	lines := []string{}
	if c.ParseError != "" {
		lines = append(lines,
			"  config file could not be read as valid JSON:",
			"  "+clipLine(c.ParseError, maxInt(8, width-4)))
		// Do not return here: the sibling hooks file is a separate
		// executable surface and stays disclosed even when config.json
		// is broken.
	}
	if c.Empty() {
		return lines
	}
	if c.MCPServerCount > 0 {
		names := strings.Join(c.MCPServerNames, ", ")
		if len(names) > 60 {
			names = names[:57] + "..."
		}
		lines = append(lines, fmt.Sprintf("  mcp servers        %d (%s)", c.MCPServerCount, names))
	}
	if c.HookCount > 0 {
		lines = append(lines, fmt.Sprintf("  hooks              %d commands on lifecycle events", c.HookCount))
	}
	if c.HookDisabledCount > 0 {
		lines = append(lines, fmt.Sprintf("  hooks (disabled)   %d defined but off", c.HookDisabledCount))
	}
	if c.HookParseError != "" {
		lines = append(lines, "  hooks              "+clipLine(c.HookParseError, maxInt(8, width-21)))
	}
	if c.ProviderCount > 0 {
		lines = append(lines, fmt.Sprintf("  providers          %d defined in project config", c.ProviderCount))
	}
	if c.ActiveProviderOverride != "" {
		lines = append(lines, "  active provider    "+c.ActiveProviderOverride)
	}
	if c.MaxTurnsOverride > 0 {
		lines = append(lines, fmt.Sprintf("  max turns          %d", c.MaxTurnsOverride))
	}
	for _, t := range c.SandboxTightening {
		lines = append(lines, "  sandbox            "+t)
	}
	if c.KeybindingOverrides > 0 {
		lines = append(lines, fmt.Sprintf("  keybindings        %d overridden", c.KeybindingOverrides))
	}
	return lines
}

// clipLine truncates s to at most n visible characters with an ellipsis.
func clipLine(s string, n int) string {
	runes := []rune(strings.TrimSpace(s))
	if n <= 0 || len(runes) <= n {
		return string(runes)
	}
	if n <= 1 {
		return string(runes[:1])
	}
	return string(runes[:n-1]) + "…"
}
