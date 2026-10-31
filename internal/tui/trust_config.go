// trust_config.go is the GAP-I project-config trust surface (contract §15.1,
// §16, Pen frame I1): when a workspace's executable project config exists but
// the workspace is untrusted, the TUI shows WHAT that config would change
// before anything loads. The existing trust picker (newTrustPicker) owns the
// trust/decline decision; this file supplies the evidence the decision
// deserves: a typed summary of the config file's executable content.
//
// Architecture fence: these are deterministic readers of the config file.
// They never decide whether the config loads — config.Resolve and the CLI's
// trusted flag own that (an untrusted workspace skips the project config, MCP
// servers, hooks, and plugins at the merge layer). The card projects; the
// runtime decides.
package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/hooks"
	"github.com/Taf0711/splice/internal/plugins"
	"github.com/Taf0711/splice/internal/projectfile"
	"github.com/Taf0711/splice/internal/specialist"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
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
	// PipelineFile is true when the sibling .splice/pipeline.json exists. The
	// file is executable topology, so its presence is disclosed.
	PipelineFile bool
	// PipelineCommands lists the command nodes of pipeline.json as
	// "name: command", in file order.
	PipelineCommands []string
	// PipelineAgents lists the agent nodes of pipeline.json as
	// "name: tools, permission, turns", in file order. An agent node runs a
	// model with the listed tools.
	PipelineAgents []string
	// PipelineParseError is non-empty when pipeline.json exists but does not
	// parse or validate as a topology.
	PipelineParseError string
	// PluginCount is the number of project plugins whose manifests parse, from
	// .splice/plugins/<name>/plugin.json. Their tools and hooks run commands once
	// the folder is trusted. Disabled plugins are counted too: the card discloses
	// what the files declare.
	PluginCount int
	// PluginCommands lists each tool and hook command of the project plugins, as
	// "plugin tool name: argv" or "plugin hook name (event): argv".
	PluginCommands []string
	// PluginParseErrors lists project plugin manifests that exist but do not
	// parse. Each entry names the manifest and the field that failed.
	PluginParseErrors []string
	// HookCommands lists each hook as "event: argv", in file order. A hook that
	// would not run is marked "(off)".
	HookCommands []string
	// MCPServerCommands lists each MCP server as "name: argv" or "name: url".
	MCPServerCommands []string
	// VerifyTestCommand is the project verify.testCommand argv, when set.
	VerifyTestCommand string
	// DecisionAdvisorBackend and DecisionAdvisorEndpoint are the project's
	// decision advisor target, when set.
	DecisionAdvisorBackend  string
	DecisionAdvisorEndpoint string
	// SpecialistNames lists the valid project specialists in .splice/specialists.
	SpecialistNames []string
	// SpecialistError is non-empty when .splice/specialists cannot be read.
	SpecialistError string
	// ConfigFile is true when the project config.json exists. False means the
	// card describes hooks or pipeline files only.
	ConfigFile bool
	// ViewFile is the file the [V] action opens (see projectTrustViewFile).
	// Empty when none of the project files exists.
	ViewFile string
}

// Empty reports whether the config carries nothing worth a trust decision.
func (c projectTrustConfig) Empty() bool {
	return c.MCPServerCount == 0 && c.HookCount == 0 && c.HookDisabledCount == 0 &&
		c.HookParseError == "" && c.ProviderCount == 0 &&
		c.ActiveProviderOverride == "" && c.MaxTurnsOverride == 0 &&
		len(c.SandboxTightening) == 0 && c.KeybindingOverrides == 0 &&
		c.ParseError == "" && !c.PipelineFile && c.PipelineParseError == "" &&
		c.PluginCount == 0 && len(c.PluginParseErrors) == 0 &&
		c.VerifyTestCommand == "" && c.DecisionAdvisorBackend == "" &&
		c.DecisionAdvisorEndpoint == "" && len(c.SpecialistNames) == 0 &&
		c.SpecialistError == ""
}

// projectTrustConfigPath returns the project config path the trust surface
// describes. config.json may be absent while hooks.json or pipeline.json still
// runs commands, so the path under the workspace root is used then. The
// function does not prove the file exists; describeProjectTrustConfig does.
func (m model) projectTrustConfigPath() string {
	if m.projectConfigPath != "" {
		return m.projectConfigPath
	}
	if strings.TrimSpace(m.cwd) == "" {
		return ""
	}
	return filepath.Join(m.cwd, ".splice", "config.json")
}

// projectTrustViewFile returns the project file the [V] action opens: the
// first of config.json, hooks.json, and pipeline.json that exists beside the
// config path. It returns "" when none exists. The card label and the action
// both call this helper, so they always name the same file.
func projectTrustViewFile(configPath string) string {
	if strings.TrimSpace(configPath) == "" {
		return ""
	}
	dir := filepath.Dir(configPath)
	for _, name := range []string{"config.json", "hooks.json", "pipeline.json"} {
		candidate := filepath.Join(dir, name)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// projectTrustViewTarget is the file the [V] action opens. It falls back to
// the config path when no project file exists, so the action keeps its
// "not found" notice in that case.
func (m model) projectTrustViewTarget() string {
	if file := projectTrustViewFile(m.projectTrustConfigPath()); file != "" {
		return file
	}
	return m.projectTrustConfigPath()
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
	out.HookCount, out.HookDisabledCount, out.HookCommands, out.HookParseError = inspectProjectHooks(filepath.Join(filepath.Dir(path), "hooks.json"))
	// pipeline.json is a sibling executable file too: its command nodes run
	// when the project topology is trusted. Disclose it like hooks.json.
	out.PipelineFile, out.PipelineCommands, out.PipelineAgents, out.PipelineParseError = inspectProjectPipeline(filepath.Join(filepath.Dir(path), "pipeline.json"))
	// Project plugins run their tool and hook commands once the folder is
	// trusted, so their manifests are disclosed the same way.
	out.PluginCount, out.PluginCommands, out.PluginParseErrors = inspectProjectPlugins(filepath.Join(filepath.Dir(path), "plugins"))
	out.SpecialistNames, out.SpecialistError = inspectProjectSpecialists(filepath.Join(filepath.Dir(path), "specialists"))
	out.ViewFile = projectTrustViewFile(path)
	data, err := projectfile.Read(path, filepath.Dir(filepath.Dir(path)))
	if err != nil {
		if os.IsNotExist(err) {
			return out
		}
		out.ConfigFile = true
		out.ParseError = err.Error()
		return out
	}
	out.ConfigFile = true
	var cfg struct {
		ActiveProvider string `json:"activeProvider"`
		MaxTurns       int    `json:"maxTurns"`
		Providers      []struct {
			Name string `json:"name"`
		} `json:"providers"`
		MCP struct {
			Servers map[string]json.RawMessage `json:"servers"`
		} `json:"mcp"`
		Hooks  json.RawMessage `json:"hooks"`
		Verify struct {
			TestCommand []string `json:"testCommand"`
		} `json:"verify"`
		DecisionAdvisor struct {
			Backend  string `json:"backend"`
			Endpoint string `json:"endpoint"`
		} `json:"decisionAdvisor"`
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
	for _, name := range out.MCPServerNames {
		out.MCPServerCommands = append(out.MCPServerCommands, mcpServerCardLine(name, cfg.MCP.Servers[name]))
	}
	if len(cfg.Verify.TestCommand) > 0 {
		out.VerifyTestCommand = cardArgv(redactedCommandArgs(cfg.Verify.TestCommand))
	}
	out.DecisionAdvisorBackend = cardText(strings.TrimSpace(cfg.DecisionAdvisor.Backend))
	out.DecisionAdvisorEndpoint = cardText(redactMCPDisplayURL(strings.TrimSpace(cfg.DecisionAdvisor.Endpoint)))
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
// how many entries would run, how many are defined but disabled, each hook's
// command, and the reader's own error when the file is unreadable.
//
// The TUI keeps no second hooks schema. The real schema is
// {"enabled": bool, "hooks": [ {..., "enabled": bool} ]}, and only the
// canonical reader is allowed to know that. A missing file is not an error:
// most workspaces have none.
func inspectProjectHooks(path string) (executable int, disabled int, commands []string, parseError string) {
	if strings.TrimSpace(path) == "" {
		return 0, 0, nil, ""
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil, ""
		}
		return 0, 0, nil, fmt.Errorf("read hooks file %s: %w", path, err).Error()
	}
	store, err := hooks.NewConfigStore(hooks.StoreOptions{ConfigPath: path})
	if err != nil {
		return 0, 0, nil, fmt.Errorf("read hooks file %s: %w", path, err).Error()
	}
	// List is read-only: it parses the single file and never writes it.
	config, err := store.List()
	if err != nil {
		return 0, 0, nil, fmt.Errorf("read hooks file %s: %w", path, err).Error()
	}
	for _, hook := range config.Hooks {
		line := cardText(string(hook.Event)) + ": " + cardArgv(redactedCommandArgs(append([]string{hook.Command}, hook.Args...)))
		if config.Enabled && hook.Enabled {
			executable++
		} else {
			disabled++
			line += " (off)"
		}
		commands = append(commands, line)
	}
	return executable, disabled, commands, ""
}

// inspectProjectPipeline reads the project topology file through the canonical
// topology loader and lists its command nodes. A missing file is not an error.
func inspectProjectPipeline(path string) (present bool, commands, agents []string, parseError string) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil, nil, ""
		}
		return true, nil, nil, fmt.Errorf("read pipeline file %s: %w", path, err).Error()
	}
	topology, err := splicerun.LoadTopologyFile(path, filepath.Dir(filepath.Dir(path)))
	if err != nil {
		return true, nil, nil, err.Error()
	}
	for _, node := range topology.Nodes {
		switch node.Type {
		case schemas.NodeTypeCommand:
			commands = append(commands, cardText(node.Name)+": "+cardArgv(node.Command))
		case schemas.NodeTypeAgent:
			agents = append(agents, cardText(splicerun.AgentNodeLine(node)))
		}
	}
	return true, commands, agents, ""
}

// inspectProjectPlugins reads the project plugins under root through the
// plugins package loader. It only parses manifests; nothing runs. A missing
// root is not an error. Each tool and hook command is listed so the trust card
// names every command the plugins can run.
func inspectProjectPlugins(root string) (count int, commands []string, parseErrors []string) {
	loaded, err := plugins.Load(plugins.LoadOptions{
		Roots: []plugins.Root{{Source: plugins.SourceProject, Path: root}},
	})
	if err != nil {
		return 0, nil, []string{cardText(err.Error())}
	}
	for _, diagnostic := range loaded.Diagnostics {
		location := diagnostic.ManifestPath
		if location == "" {
			location = diagnostic.Root
		}
		message := diagnostic.Message
		if diagnostic.FieldPath != "" {
			message = diagnostic.FieldPath + ": " + message
		}
		parseErrors = append(parseErrors, cardText(location)+": "+cardText(message))
	}
	for _, plugin := range loaded.Plugins {
		for _, tool := range plugin.Tools {
			commands = append(commands, cardText(plugin.Name)+" tool "+cardText(tool.Name)+": "+cardArgv(append([]string{tool.Command}, tool.Args...)))
		}
		for _, hook := range plugin.Hooks {
			commands = append(commands, cardText(plugin.Name)+" hook "+cardText(hook.Name)+" ("+cardText(string(hook.Event))+"): "+cardArgv(append([]string{hook.Command}, hook.Args...)))
		}
	}
	return len(loaded.Plugins), commands, parseErrors
}

// inspectProjectSpecialists lists the valid specialist manifests in the project
// specialists directory. A missing directory is not an error.
func inspectProjectSpecialists(dir string) (names []string, errText string) {
	manifests, _, err := specialist.LoadProjectDir(dir)
	if err != nil {
		return nil, cardText(err.Error())
	}
	for _, manifest := range manifests {
		names = append(names, cardText(manifest.Metadata.Name))
	}
	sort.Strings(names)
	return names, ""
}

// mcpServerCardLine describes one MCP server for the trust card: its command
// and args, or its URL for a remote server. Secrets are redacted as the MCP
// status view redacts them.
func mcpServerCardLine(name string, raw json.RawMessage) string {
	var server struct {
		Command  string   `json:"command"`
		Args     []string `json:"args"`
		URL      string   `json:"url"`
		Disabled bool     `json:"disabled"`
	}
	label := cardText(name)
	if err := json.Unmarshal(raw, &server); err != nil {
		return label + ": (unreadable entry)"
	}
	var target string
	switch {
	case server.Command != "":
		target = cardArgv(redactedCommandArgs(append([]string{server.Command}, server.Args...)))
	case server.URL != "":
		target = "url " + cardText(redactMCPDisplayURL(server.URL))
	default:
		target = "no command or url"
	}
	if server.Disabled {
		target += " (off)"
	}
	return label + ": " + target
}

// cardArgv joins an argv for the trust card. Each argument is made safe for
// the card with cardText.
func cardArgv(argv []string) string {
	return cardText(stages.CommandLine(argv))
}

// cardText makes untrusted project text safe for the NUL and \x1f delimited
// trust card: terminal control sequences go, and so do the delimiters.
func cardText(s string) string {
	return strings.NewReplacer("\x00", "", "\x1f", "").Replace(sanitizeTerminalOutput(s, false))
}

// countHookEntries returns the number of hook commands the file at path
// would actually run. It delegates to the canonical reader through
// inspectProjectHooks; a disabled file or a disabled entry counts zero.
func countHookEntries(path string) int {
	executable, _, _, _ := inspectProjectHooks(path)
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
	// A zero width is the transcript payload: keep every parse error whole.
	clip := func(text string, n int) string {
		if width <= 0 {
			return text
		}
		return clipLine(text, n)
	}
	if c.ParseError != "" {
		lines = append(lines,
			"  config file could not be read as valid JSON:",
			"  "+clip(c.ParseError, maxInt(8, width-4)))
		// Do not return here: the sibling hooks file is a separate
		// executable surface and stays disclosed even when config.json
		// is broken.
	}
	if c.Empty() {
		return lines
	}
	if c.MCPServerCount > 0 {
		names := cardText(strings.Join(c.MCPServerNames, ", "))
		if len(names) > 60 {
			names = names[:57] + "..."
		}
		lines = append(lines, trustCardRow("mcp servers", fmt.Sprintf("%d (%s)", c.MCPServerCount, names)))
		lines = append(lines, trustCardList("mcp server", c.MCPServerCommands)...)
	}
	if c.HookCount > 0 {
		lines = append(lines, trustCardRow("hooks", pluralizeCount(c.HookCount, "command on lifecycle events", "commands on lifecycle events")))
	}
	if c.HookDisabledCount > 0 {
		lines = append(lines, trustCardRow("hooks (disabled)", fmt.Sprintf("%d defined but off", c.HookDisabledCount)))
	}
	if c.HookParseError != "" {
		lines = append(lines, trustCardRow("hooks", clip(c.HookParseError, maxInt(8, width-21))))
	}
	lines = append(lines, trustCardList("hook command", c.HookCommands)...)
	if c.ProviderCount > 0 {
		lines = append(lines, trustCardRow("providers", fmt.Sprintf("%d defined in project config", c.ProviderCount)))
	}
	if c.ActiveProviderOverride != "" {
		lines = append(lines, trustCardRow("active provider", c.ActiveProviderOverride))
	}
	if c.MaxTurnsOverride > 0 {
		lines = append(lines, trustCardRow("max turns", fmt.Sprintf("%d", c.MaxTurnsOverride)))
	}
	for _, t := range c.SandboxTightening {
		lines = append(lines, trustCardRow("sandbox", t))
	}
	if c.KeybindingOverrides > 0 {
		lines = append(lines, trustCardRow("keybindings", fmt.Sprintf("%d overridden", c.KeybindingOverrides)))
	}
	if c.VerifyTestCommand != "" {
		lines = append(lines, trustCardRow("verify testCommand", c.VerifyTestCommand))
	}
	if c.DecisionAdvisorBackend != "" || c.DecisionAdvisorEndpoint != "" {
		var parts []string
		if c.DecisionAdvisorBackend != "" {
			parts = append(parts, "backend "+c.DecisionAdvisorBackend)
		}
		if c.DecisionAdvisorEndpoint != "" {
			parts = append(parts, "endpoint "+clip(c.DecisionAdvisorEndpoint, maxInt(8, width-40)))
		}
		lines = append(lines, trustCardRow("decision advisor", strings.Join(parts, ", ")))
	}
	if c.PipelineParseError != "" {
		lines = append(lines, trustCardRow("pipeline", clip(c.PipelineParseError, maxInt(8, width-21))))
	} else if c.PipelineFile {
		summary := pluralizeCount(len(c.PipelineCommands), "command node", "command nodes")
		if len(c.PipelineAgents) > 0 {
			summary += ", " + pluralizeCount(len(c.PipelineAgents), "agent node", "agent nodes")
		}
		lines = append(lines, trustCardRow("pipeline", summary+" in .splice/pipeline.json"))
		lines = append(lines, trustCardList("command node", c.PipelineCommands)...)
		lines = append(lines, trustCardList("agent node", c.PipelineAgents)...)
	}
	parseErrors := make([]string, 0, len(c.PluginParseErrors))
	for _, parseError := range c.PluginParseErrors {
		parseErrors = append(parseErrors, clip(parseError, maxInt(8, width-21)))
	}
	lines = append(lines, trustCardList("plugin", parseErrors)...)
	if c.PluginCount > 0 {
		lines = append(lines, trustCardRow("plugins", pluralizeCount(c.PluginCount, "project plugin", "project plugins")+" in .splice/plugins"))
		lines = append(lines, trustCardList("plugin command", c.PluginCommands)...)
	}
	if c.SpecialistError != "" {
		lines = append(lines, trustCardRow("specialists", clip(c.SpecialistError, maxInt(8, width-21))))
	} else if len(c.SpecialistNames) > 0 {
		lines = append(lines, trustCardRow("specialists", pluralizeCount(len(c.SpecialistNames), "project specialist", "project specialists")+" in .splice/specialists"))
		lines = append(lines, trustCardList("specialist", c.SpecialistNames)...)
	}
	return lines
}

// trustCardListLimit caps each command or name list on the trust card. Entries
// past the cap collapse into one "and N more" row.
const trustCardListLimit = 20

// trustCardRow formats one trust card row. The label sits in a fixed column.
func trustCardRow(label, value string) string {
	return fmt.Sprintf("  %-18s %s", label, value)
}

// trustCardList renders up to trustCardListLimit rows, one per item, then an
// "and N more" row for the rest.
func trustCardList(label string, items []string) []string {
	lines := []string{}
	for i, item := range items {
		if i == trustCardListLimit {
			lines = append(lines, trustCardRow("", fmt.Sprintf("and %d more", len(items)-i)))
			break
		}
		lines = append(lines, trustCardRow(label, item))
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
