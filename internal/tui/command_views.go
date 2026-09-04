package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/providercatalog"
	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/tools"
	"github.com/Taf0711/splice/internal/zerocommands"
)

// toolsSourceGroup is one per-source grouping of the registered tool catalog:
// the built-in tools, or the tools owned by one MCP server.
type toolsSourceGroup struct {
	// Source is the display source label: "BUILTIN", the MCP server name, or
	// "(unknown source)" when a registered MCP tool cannot be attributed to a
	// configured server. Never invent a server name — unattributable tools land
	// here so the card stays honest.
	Source string
	// Names are the tool names in the group, alpha-sorted, in registry form.
	Names []string
	// Hidden counts tools the run defers behind tool_search; 0 when none.
	Hidden int
	// State is the MCP connection state from the MCP view ("connected",
	// "degraded", "disabled", "enabled"); empty for BUILTIN.
	State string
}

const toolsUnknownSourceLabel = "(unknown source)"

// toolsGroupBySource partitions the registered tools into per-source groups.
// Builtin tools (no "mcp_" prefix) form the BUILTIN group. MCP tools are
// attributed via MCPServerName() when the tool reports it, else via the server
// token in the "mcp_<server>_<tool>" registry name matched against the
// configured MCP servers. Anything unattributable goes to "(unknown source)".
// hidden counts deferred MCP tools whose schemas the run withholds behind
// tool_search (agent.Options.DeferThreshold > 0 with more eligible tools than
// the threshold); the card surfaces them as "degraded — N tools hidden" so the
// registration list never overstates what the model can call without loading.
func toolsGroupBySource(registered []tools.Tool, state MCPViewState, deferThreshold int) []toolsSourceGroup {
	builtinNames := []string{}
	serverTools := map[string][]string{}
	var unknownNames []string

	sortedServers := make([]string, 0, len(state.Servers))
	for _, server := range state.Servers {
		sortedServers = append(sortedServers, server.Name)
	}
	sort.Strings(sortedServers)

	serverTokenMap := mcpServerTokens(state)

	// eligible counts deferral-eligible MCP tools so hidden can be derived
	// against the run's DeferThreshold, mirroring the agent loop's gate.
	eligible := 0
	for _, tool := range registered {
		if tools.IsDeferralEligible(tool) {
			eligible++
		}
	}
	deferActive := deferThreshold > 0 && eligible >= deferThreshold

	// A deferred MCP tool's schema is withheld: count it as hidden. Deferral
	// only applies to deferred-eligible tools; builtin tools are always eager.
	deferredEligibleNames := map[string]bool{}
	if deferActive {
		for _, tool := range registered {
			if tools.IsDeferralEligible(tool) {
				deferredEligibleNames[tool.Name()] = true
			}
		}
	}

	for _, tool := range registered {
		name := tool.Name()
		if !strings.HasPrefix(name, "mcp_") {
			builtinNames = append(builtinNames, name)
			continue
		}
		if deferredEligibleNames[name] {
			continue
		}
		server := toolsServerFor(tool, state, serverTokenMap)
		if server == "" {
			unknownNames = append(unknownNames, name)
			continue
		}
		serverTools[server] = append(serverTools[server], name)
	}
	sort.Strings(builtinNames)
	for _, names := range serverTools {
		sort.Strings(names)
	}

	groups := make([]toolsSourceGroup, 0, 1+len(state.Servers)+1)
	if len(builtinNames) > 0 {
		groups = append(groups, toolsSourceGroup{Source: "BUILTIN", Names: builtinNames})
	}
	for _, server := range sortedServers {
		view := mcpServerViewByName(state, server)
		group := toolsSourceGroup{
			Source: server,
			Names:  serverTools[server],
			State:  toolsServerStateLabel(view),
		}
		hidden := 0
		for _, tool := range registered {
			if !deferredEligibleNames[tool.Name()] {
				continue
			}
			if toolsServerFor(tool, state, serverTokenMap) == server {
				hidden++
			}
		}
		group.Hidden = hidden
		if len(group.Names) > 0 || hidden > 0 {
			groups = append(groups, group)
		}
	}
	if len(unknownNames) > 0 {
		sort.Strings(unknownNames)
		groups = append(groups, toolsSourceGroup{Source: toolsUnknownSourceLabel, Names: unknownNames})
	}
	return groups
}

// mcpServerTokens maps each configured server's sanitized name token (the token
// registryToolName embeds in "mcp_<server>_<tool>") to the server's real name.
func mcpServerTokens(state MCPViewState) map[string]string {
	tokens := make(map[string]string, len(state.Servers))
	for _, server := range state.Servers {
		tokens[mcpStateSanitizeToolNamePart(server.Name)] = server.Name
	}
	return tokens
}

// toolsServerFor resolves a registered MCP tool's owning server name: the
// tool's own MCPServerName() report when present, else the configured server
// whose sanitized token the registry name carries, else "" (unknown).
func toolsServerFor(tool tools.Tool, state MCPViewState, serverTokenMap map[string]string) string {
	if named, ok := tool.(mcpServerNamedTool); ok {
		if reported := strings.TrimSpace(named.MCPServerName()); reported != "" && mcpServerViewByName(state, reported) != nil {
			return reported
		}
	}
	rest, ok := strings.CutPrefix(tool.Name(), "mcp_")
	if !ok {
		return ""
	}
	tokens := make([]string, 0, len(serverTokenMap))
	for token := range serverTokenMap {
		tokens = append(tokens, token)
	}
	sort.Slice(tokens, func(left, right int) bool {
		if len(tokens[left]) != len(tokens[right]) {
			return len(tokens[left]) > len(tokens[right])
		}
		return tokens[left] < tokens[right]
	})
	for _, token := range tokens {
		if strings.HasPrefix(rest, token+"_") {
			return serverTokenMap[token]
		}
	}
	return ""
}

func mcpServerViewByName(state MCPViewState, name string) *MCPServerView {
	for index := range state.Servers {
		if state.Servers[index].Name == name {
			return &state.Servers[index]
		}
	}
	return nil
}

// toolsServerStateLabel picks the connection label for a server section header:
// "degraded" when the run defers tools behind tool_search, otherwise the
// configured state ("disabled" or "connected" for an enabled server).
func toolsServerStateLabel(view *MCPServerView) string {
	if view == nil {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(view.State), "disabled") {
		return "disabled"
	}
	return "connected"
}

// toolsSummaryLine joins a section's state words ("degraded — 2 tools hidden")
// with the frame's em-dash separator. Sits on the section title line.
func toolsSummaryLine(group toolsSourceGroup) string {
	parts := []string{}
	switch {
	case group.Hidden > 0:
		parts = append(parts, "degraded", fmt.Sprintf("— %d tools hidden", group.Hidden))
	case group.State != "":
		parts = append(parts, group.State)
	}
	return strings.Join(parts, " ")
}

// toolsRenderSectionLines renders a group's tool names the way the frame lays
// them out: four per wrapped line, double-spaced, aligned under the header.
func toolsRenderSectionLines(group toolsSourceGroup) []string {
	lines := make([]string, 0, 1+len(group.Names)/4+1)
	if summary := toolsSummaryLine(group); summary != "" {
		lines = append(lines, summary)
	}
	const perLine = 4
	for start := 0; start < len(group.Names); start += perLine {
		end := start + perLine
		if end > len(group.Names) {
			end = len(group.Names)
		}
		lines = append(lines, strings.Join(group.Names[start:end], "  "))
	}
	return lines
}

// toolsHints renders the registration-vs-access note the /tools frame carries:
// registration only lists a tool; /permissions decides what runs.
func toolsHints(groups []toolsSourceGroup) []string {
	return []string{"every tool here is gated by /permissions — registration is not access"}
}

func (m model) toolsText() string {
	registered := m.registeredTools()
	count := len(registered)
	if count == 0 {
		return renderCommandCardTranscript(commandCard{
			Title:   "Tools",
			Summary: []string{"0 registered", "no tools available"},
			Sections: []commandCardSection{{
				Title: "Registry",
				Fields: []commandField{
					{Key: "registered", Value: "0"},
				},
			}},
			Actions: []string{"/mcp manage servers", "/permissions manage access"},
		})
	}

	state := m.mcpViewState()
	groups := toolsGroupBySource(registered, state, m.agentOptions.DeferThreshold)

	sections := make([]commandCardSection, 0, len(groups))
	for _, group := range groups {
		sections = append(sections, commandCardSection{
			Title: group.Source,
			Lines: toolsRenderSectionLines(group),
		})
	}

	summary := fmt.Sprintf("%d registered", count)
	if len(groups) > 1 {
		summary = fmt.Sprintf("%d registered · %d sources", count, len(groups))
	}

	return renderCommandCardTranscript(commandCard{
		Title:    "Tools",
		Summary:  []string{summary, "registered catalog"},
		Sections: sections,
		Actions:  []string{"/mcp manage servers", "/permissions manage access"},
		Hints:    toolsHints(groups),
	})
}

func (m *model) mcpText() string {
	width := 0
	if m.width > 0 {
		width = chatWidth(m.width)
	}
	return renderMCPView(m.mcpViewState(), width)
}

func (m *model) refreshMCPViewState() {
	m.mcpViewStateCache = BuildMCPViewState(MCPStateOptions{
		Config:          m.mcpConfig,
		Registry:        m.registry,
		PermissionStore: m.mcpPermissionStore,
		PermissionMode:  string(m.permissionMode),
		TokenStore:      m.mcpTokenStore,
	})
	m.mcpViewStateReady = true
}

func (m *model) mcpViewState() MCPViewState {
	if m.mcpViewStateReady {
		return m.mcpViewStateCache
	}
	// Older tests may construct a splice-value model; keep that path useful, while
	// production refreshes the cache before any MCP view can render.
	m.refreshMCPViewState()
	return m.mcpViewStateCache
}

func (m model) startMCPTranscriptCommand(args string) (model, tea.Cmd) {
	args = strings.TrimSpace(args)
	if args == "" {
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, tool: "mcp", text: m.mcpText()})
		return m, nil
	}
	parsedArgs, err := splitMCPCommandArgs(args)
	if err != nil {
		text := strings.Join([]string{
			"MCP action failed",
			err.Error(),
			"",
			m.mcpText(),
		}, "\n")
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, tool: "mcp", text: text})
		return m, nil
	}
	return m.startMCPCommand(mcpCommandRequest{origin: mcpCommandOriginTranscript, raw: args, args: parsedArgs})
}

func (m model) startMCPCommand(request mcpCommandRequest) (model, tea.Cmd) {
	if m.mcpCommand == nil {
		result := MCPCommandResult{
			ExitCode: 1,
			Error:    "MCP action unavailable",
			Config:   m.mcpConfig,
		}
		return m.applyMCPCommandResultMessage(mcpCommandResultMsg{request: request, result: result}), nil
	}
	m.cancelMCPCommand()
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	m.mcpCommandSeq++
	request.id = m.mcpCommandSeq
	request.args = append([]string{}, request.args...)
	m.mcpCommandCancel = cancel
	runner := m.mcpCommand
	return m, func() tea.Msg {
		return mcpCommandResultMsg{
			request: request,
			result:  runner(ctx, request.args),
		}
	}
}

func (m *model) cancelMCPCommand() {
	if m.mcpCommandCancel != nil {
		m.mcpCommandCancel()
		m.mcpCommandCancel = nil
		m.mcpCommandSeq++
	}
}

func (m model) applyMCPCommandResultMessage(msg mcpCommandResultMsg) model {
	if msg.request.id != 0 && msg.request.id != m.mcpCommandSeq {
		return m
	}
	m.mcpCommandCancel = nil
	switch msg.request.origin {
	case mcpCommandOriginManager:
		text := ""
		m, text = m.applyMCPCommandResult(strings.Join(msg.request.args, " "), msg.result)
		m.mcpManager = &mcpManagerState{selected: msg.request.managerSelected, query: msg.request.managerQuery}
		if items := m.mcpManagerItems(); len(items) > 0 {
			m.mcpManager.selected = clampInt(m.mcpManager.selected, 0, len(items)-1)
		}
		if text != "" {
			m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, tool: "mcp", text: text})
		}
	case mcpCommandOriginWizard:
		m = m.applyMCPAddWizardSaveResult(msg.result, msg.request.wizardDisabled)
	default:
		text := ""
		m, text = m.applyMCPCommandResult(msg.request.raw, msg.result)
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, tool: "mcp", text: text})
	}
	return m
}

func (m model) applyMCPCommandResult(args string, result MCPCommandResult) (model, string) {
	if result.ExitCode != 0 || strings.TrimSpace(result.Error) != "" {
		message := strings.TrimSpace(result.Error)
		if message == "" {
			message = strings.TrimSpace(result.Output)
		}
		if message == "" {
			message = "MCP command failed"
		}
		return m, strings.Join([]string{
			"MCP action failed",
			message,
			"",
			m.mcpText(),
		}, "\n")
	}
	if len(result.Config.Servers) > 0 || len(m.mcpConfig.Servers) > 0 {
		m.mcpConfig = result.Config
		m.refreshMCPViewState()
	}
	output := strings.TrimSpace(result.Output)
	if output == "" {
		output = "splice mcp " + args
	}
	return m, strings.Join([]string{
		"MCP action complete",
		output,
		"",
		m.mcpText(),
	}, "\n")
}

func splitMCPCommandArgs(args string) ([]string, error) {
	args = strings.TrimSpace(args)
	if args == "" {
		return nil, nil
	}
	out := []string{}
	var current strings.Builder
	var quote rune
	hasToken := false
	runes := []rune(args)
	for index := 0; index < len(runes); index++ {
		r := runes[index]
		if quote != 0 {
			if r == quote {
				quote = 0
				hasToken = true
				continue
			}
			if r == '\\' && index+1 < len(runes) && runes[index+1] == quote {
				index++
				current.WriteRune(runes[index])
				hasToken = true
				continue
			}
			current.WriteRune(r)
			hasToken = true
			continue
		}
		switch {
		case r == '\'' || r == '"':
			quote = r
			hasToken = true
		case r == '\\' && index+1 < len(runes) && (runes[index+1] == '\'' || runes[index+1] == '"' || strings.TrimSpace(string(runes[index+1])) == ""):
			index++
			current.WriteRune(runes[index])
			hasToken = true
		case strings.TrimSpace(string(r)) == "":
			if hasToken {
				out = append(out, current.String())
				current.Reset()
				hasToken = false
			}
		default:
			current.WriteRune(r)
			hasToken = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in MCP command")
	}
	if hasToken {
		out = append(out, current.String())
	}
	return out, nil
}

func (m model) permissionsText() string {
	if m.sandboxStore == nil {
		return m.permissionsTextWithStore(nil)
	}
	return m.permissionsTextWithStore(m.sandboxStore)
}

func (m model) permissionsTextWithStore(store grantLister) string {
	mode := string(m.permissionMode)
	if store == nil {
		return renderCommandCardTranscript(commandCard{
			Title:   "Permissions",
			Summary: []string{mode + " permissions", "grants unavailable"},
			Sections: []commandCardSection{
				{
					Title: "State",
					Fields: []commandField{
						{Key: "mode", Value: mode},
					},
				},
				{
					Title: "Grants",
					Lines: []string{"persistent grants: unavailable"},
				},
			},
		})
	}

	grants, err := store.List()
	if err != nil {
		return renderCommandCardTranscript(commandCard{
			Title:   "Permissions",
			Summary: []string{mode + " permissions", "grants error"},
			Sections: []commandCardSection{
				{
					Title: "State",
					Fields: []commandField{
						{Key: "mode", Value: mode},
					},
				},
				{
					Title: "Grants",
					Lines: []string{"error: " + err.Error()},
				},
			},
		})
	}
	prefixes := []sandbox.CommandPrefixGrant{}
	if prefixStore, ok := store.(commandPrefixGrantLister); ok {
		var prefixErr error
		prefixes, prefixErr = prefixStore.ListCommandPrefixes()
		if prefixErr != nil {
			return renderCommandCardTranscript(commandCard{
				Title:   "Permissions",
				Summary: []string{mode + " permissions", "grants error"},
				Sections: []commandCardSection{
					{
						Title: "State",
						Fields: []commandField{
							{Key: "mode", Value: mode},
						},
					},
					{
						Title: "Grants",
						Lines: []string{"error: " + prefixErr.Error()},
					},
				},
			})
		}
	}

	snapshots := zerocommands.SandboxGrantSnapshots(grants)
	grantRows := []commandRow{}
	if len(snapshots) == 0 && len(prefixes) == 0 {
		grantRows = append(grantRows, commandRow{Text: "none"})
	} else {
		for _, grant := range snapshots {
			line := fmt.Sprintf("%s [%s]", grant.ToolName, grant.Decision)
			if grant.ApprovedAt != "" {
				line += " approved " + grant.ApprovedAt
			}
			if grant.Reason != "" {
				line += " - " + grant.Reason
			}
			grantRows = append(grantRows, commandRow{Text: line})
		}
		for _, grant := range prefixes {
			line := fmt.Sprintf("%s `%s` [command-prefix]", grant.ToolName, strings.Join(grant.Prefix, " "))
			if grant.ApprovedAt != "" {
				line += " approved " + grant.ApprovedAt
			}
			if grant.Reason != "" {
				line += " - " + grant.Reason
			}
			grantRows = append(grantRows, commandRow{Text: line})
		}
	}

	return renderCommandCardTranscript(commandCard{
		Title:   "Permissions",
		Summary: []string{mode + " permissions", formatGrantCount(len(snapshots) + len(prefixes))},
		Sections: []commandCardSection{
			{
				Title: "State",
				Fields: []commandField{
					{Key: "mode", Value: mode},
				},
			},
			{
				Title: "Grants",
				Rows:  grantRows,
			},
		},
	})
}

// grantLister is the subset of sandbox.GrantStore used by permissionsText().
// It exists to let tests inject error-stub stores without reaching for a real
// filesystem path.
type grantLister interface {
	List() ([]sandbox.Grant, error)
}

type commandPrefixGrantLister interface {
	ListCommandPrefixes() ([]sandbox.CommandPrefixGrant, error)
}

func formatGrantCount(count int) string {
	if count == 0 {
		return "no persistent grants"
	}
	if count == 1 {
		return "1 persistent grant"
	}
	return fmt.Sprintf("%d persistent grants", count)
}

func (m model) providerText() string {
	profileLines := []string{
		"provider: " + displayValue(m.providerName, "none"),
		"model: " + displayValue(m.modelName, "none"),
	}
	if !config.HasProviderProfile(m.providerProfile) {
		profileLines = append(profileLines, "profile: not configured")
		return renderCommandOutput(commandOutput{
			Title:  "Provider",
			Status: commandStatusWarning,
			Sections: []commandSection{
				{Title: "Active", Lines: profileLines},
				{Title: "Next actions", Lines: []string{
					"splice providers catalog",
					"splice providers setup openai --set-active",
					"splice providers add openai --api-key-env OPENAI_API_KEY --set-active",
				}},
			},
		})
	}

	snapshot := zerocommands.ProviderSnapshotFromProfile(m.providerProfile, true)
	profileLines = append(profileLines,
		"active: "+boolText(snapshot.Active),
		"kind: "+displayValue(snapshot.ProviderKind, "unknown"),
		"api model: "+displayValue(snapshot.APIModel, "unknown"),
		"base url: "+displayValue(snapshot.BaseURL, "default"),
		"api key: "+apiKeyState(snapshot.APIKeySet),
	)
	if snapshot.Message != "" {
		profileLines = append(profileLines, "provider status: "+snapshot.Status+" - "+snapshot.Message)
	}

	status := commandStatusOK
	actionLines := providerNextActionLines(m.providerProfile, snapshot, m.providerName)
	if providerCredentialRequired(m.providerProfile, snapshot.ProviderKind) && !providerProfileHasCredential(m.providerProfile) {
		status = commandStatusWarning
	}
	return renderCommandOutput(commandOutput{
		Title:  "Provider",
		Status: status,
		Sections: []commandSection{
			{Title: "Active", Lines: profileLines},
			{Title: "Next actions", Lines: actionLines},
		},
	})
}

func providerNextActionLines(profile config.ProviderProfile, snapshot zerocommands.ProviderSnapshot, activeName string) []string {
	providerName := firstProviderDisplayValue(snapshot.Name, activeName, profile.Name, providerSetupCatalogID(profile, snapshot.ProviderKind), "openai")
	setupID := providerSetupCatalogID(profile, snapshot.ProviderKind)
	lines := []string{}
	if providerCredentialRequired(profile, snapshot.ProviderKind) && !providerProfileHasCredential(profile) {
		if envName := providerCredentialEnvName(profile, snapshot.ProviderKind); envName != "" {
			lines = append(lines,
				"set "+envName+" in your environment",
				"splice providers add "+setupID+" --api-key-env "+envName+" --set-active",
			)
		} else {
			lines = append(lines, "set provider credentials in your environment")
		}
	}
	return append(lines,
		"splice providers check "+providerName+" --connectivity",
		"splice providers catalog",
		"splice providers setup "+setupID+" --set-active",
	)
}

func providerProfileHasCredential(profile config.ProviderProfile) bool {
	return profile.HasConfiguredCredential()
}

func providerCredentialRequired(profile config.ProviderProfile, providerKind string) bool {
	if descriptor, ok := providerCatalogDescriptor(profile); ok {
		return descriptor.RequiresAuth
	}
	switch config.ProviderKind(strings.TrimSpace(providerKind)) {
	case config.ProviderKindOpenAI, config.ProviderKindOpenAICompatible, config.ProviderKindAnthropic, config.ProviderKindAnthropicCompat, config.ProviderKindGoogle:
		return true
	default:
		return false
	}
}

func providerCredentialEnvName(profile config.ProviderProfile, providerKind string) string {
	if envName := strings.TrimSpace(profile.APIKeyEnv); envName != "" {
		return envName
	}
	if descriptor, ok := providerCatalogDescriptor(profile); ok && len(descriptor.AuthEnvVars) > 0 {
		return descriptor.AuthEnvVars[0]
	}
	switch config.ProviderKind(strings.TrimSpace(providerKind)) {
	case config.ProviderKindOpenAI, config.ProviderKindOpenAICompatible:
		return "OPENAI_API_KEY"
	case config.ProviderKindAnthropic, config.ProviderKindAnthropicCompat:
		return "ANTHROPIC_API_KEY"
	case config.ProviderKindGoogle:
		return "GEMINI_API_KEY"
	default:
		return ""
	}
}

func providerSetupCatalogID(profile config.ProviderProfile, providerKind string) string {
	if catalogID := strings.TrimSpace(profile.CatalogID); catalogID != "" {
		return catalogID
	}
	switch config.ProviderKind(strings.TrimSpace(providerKind)) {
	case config.ProviderKindOpenAI:
		return "openai"
	case config.ProviderKindAnthropic:
		return "anthropic"
	case config.ProviderKindGoogle:
		return "google"
	case config.ProviderKindOpenAICompatible:
		return "custom-openai-compatible"
	case config.ProviderKindAnthropicCompat:
		return "custom-anthropic-compatible"
	default:
		return firstProviderDisplayValue(profile.Name, "openai")
	}
}

func providerCatalogDescriptor(profile config.ProviderProfile) (providercatalog.Descriptor, bool) {
	catalogID := strings.TrimSpace(profile.CatalogID)
	if catalogID == "" {
		return providercatalog.Descriptor{}, false
	}
	descriptor, err := providercatalog.Require(catalogID)
	if err != nil {
		return providercatalog.Descriptor{}, false
	}
	return descriptor, true
}

func firstProviderDisplayValue(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func (m model) modelText(args string) string {
	return renderCommandOutput(commandOutput{
		Title:  "Model",
		Status: commandStatusOK,
		Sections: []commandSection{{
			Title: "Active",
			Lines: []string{
				"model: " + displayValue(m.modelName, "none"),
				"provider: " + displayValue(m.providerName, "none"),
				"effort: " + m.effortDisplay(),
			},
		}},
		Hints: []string{"use /model list to inspect models or /model <id> to switch this TUI session"},
	})
}

// avgTurnLatencyText reports the session's rolling average turn wall-time for
// /context, the "is it slow?" signal a user otherwise can only feel. "n/a" until
// a turn has completed.
func (m model) avgTurnLatencyText() string {
	if m.turnLatencyCount == 0 {
		return "n/a"
	}
	avgSeconds := m.turnLatencySum.Seconds() / float64(m.turnLatencyCount)
	clauses := []string{}
	if m.turnTTFTCount > 0 {
		ttftSeconds := m.turnTTFTSum.Seconds() / float64(m.turnTTFTCount)
		clauses = append(clauses, fmt.Sprintf("%.1fs to first token", ttftSeconds))
		generation := m.turnLatencySum - m.turnTTFTSum
		if m.turnVisibleOutputTokens > 0 && m.turnTTFTCount == m.turnLatencyCount && generation > 0 {
			throughput := float64(m.turnVisibleOutputTokens) / generation.Seconds()
			clauses = append(clauses, fmt.Sprintf("%.1f TPS avg across turns", throughput))
		}
	}
	clauses = append(clauses, fmt.Sprintf("%d turns", m.turnLatencyCount))
	return fmt.Sprintf("%.1fs avg (%s)", avgSeconds, strings.Join(clauses, ", "))
}

func (m model) contextText() string {
	toolCount := len(m.registeredTools())
	return renderCommandCardTranscript(commandCard{
		Title: "Context",
		Summary: []string{
			"go runtime",
			string(m.permissionMode) + " permissions",
			pluralizeCount(toolCount, "tool", "tools"),
		},
		Sections: []commandCardSection{
			{
				Title: "Runtime",
				Fields: []commandField{
					{Key: "cwd", Value: displayValue(m.cwd, "unknown")},
					{Key: "provider", Value: displayValue(m.providerName, "none")},
					{Key: "model", Value: displayValue(m.modelName, "none")},
					{Key: "effort", Value: m.effortDisplay()},
					{Key: "style", Value: displayValue(m.responseStyle, defaultResponseStyle)},
					{Key: "usage", Value: m.usageSummaryText()},
					{Key: "cache", Value: m.cacheEfficiencyText()},
					{Key: "latency", Value: m.avgTurnLatencyText()},
					{Key: "max turns", Value: fmt.Sprint(m.agentOptions.MaxTurns)},
				},
			},
			{
				Title: "Session",
				Fields: []commandField{
					{Key: "active", Value: displayValue(m.activeSession.SessionID, "none")},
					{Key: "root", Value: displayValue(m.sessionRootDir(), "unknown")},
					{Key: "compaction", Value: contextCompactionStatus(m.compactionStatus())},
				},
			},
			{
				Title: "Tools",
				Fields: []commandField{
					{Key: "registered", Value: fmt.Sprint(toolCount)},
				},
			},
		},
		Actions: []string{"/permissions manage access", "/tools inspect catalog"},
	})
}

// applyTrustPickerChoice saves the selected action and updates the live trust
// indicator. Project resources keep the startup decision until the next launch.
// The [V] view-config row is handled BEFORE the unknown-choice guard: it is an
// inspection action, not a trust decision — it returns to the menu unchanged.
func (m model) applyTrustPickerChoice(item pickerItem) (model, string, bool) {
	if item.Value == trustActionView {
		// Re-open the trust menu after the editor exits, and append the
		// evidence card so the summary stays in the transcript.
		cfg := describeProjectTrustConfig(m.projectConfigPath)
		m.trustConfigNotice(cfg)
		return m, "", false
	}
	target := m.cwd
	trusted := item.Value != trustActionDecline
	if item.Value == trustActionParent {
		target = filepath.Dir(m.cwd)
	} else if item.Value != trustActionCurrent && item.Value != trustActionDecline {
		return m, "Unknown trust choice.", false
	}

	if m.trustStore == nil {
		return m, "Failed to save workspace trust decision: trust store unavailable.", false
	}
	if err := m.trustStore.SetTrusted(target, trusted); err != nil {
		return m, "Failed to save workspace trust decision: " + err.Error(), false
	}
	if item.Value == trustActionParent {
		// An earlier decline for this folder is more specific than parent trust.
		// Replace it so the parent choice also applies to this folder next time.
		if err := m.trustStore.SetTrusted(m.cwd, true); err != nil {
			return m, "Failed to save workspace trust decision: " + err.Error(), false
		}
	}
	if err := m.trustStore.Save(); err != nil {
		return m, "Failed to save workspace trust decision: " + err.Error(), false
	}

	m.trusted = trusted
	m.agentOptions.TrustedWorkspace = trusted
	if !trusted {
		return m, "Workspace trust declined. This session stays untrusted.", true
	}
	return m, "Workspace trust saved. Project resources load on the next launch.", true
}

func (m model) registeredTools() []tools.Tool {
	if m.registry == nil {
		return nil
	}
	return m.registry.All()
}

func (m model) sessionRootDir() string {
	if m.sessionStore == nil {
		return ""
	}
	return m.sessionStore.RootDir
}

func pluralizeCount(count int, singular string, plural string) string {
	label := plural
	if count == 1 {
		label = singular
	}
	return fmt.Sprintf("%d %s", count, label)
}

func contextCompactionStatus(status string) string {
	if status == "not compacted" {
		return "idle"
	}
	return status
}

// onOff renders a boolean preference as "on"/"off" for config display.
func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func (m model) configText() string {
	return renderCommandOutput(commandOutput{
		Title:  "Config",
		Status: commandStatusOK,
		Sections: []commandSection{
			{
				Title: "Runtime",
				Lines: []string{
					"runtime: go",
					fmt.Sprintf("max turns: %d", m.agentOptions.MaxTurns),
					"permission mode: " + string(m.permissionMode),
					"recaps: " + onOff(m.recapsEnabled),
				},
			},
			{
				Title: "Provider",
				Lines: []string{
					"provider: " + displayValue(m.providerName, "none"),
					"model: " + displayValue(m.modelName, "none"),
					"api key: " + apiKeyState(strings.TrimSpace(m.providerProfile.APIKey) != "" || m.providerProfile.APIKeyStored),
				},
			},
		},
	})
}

func (m model) debugText() string {
	state := "idle"
	if m.pending {
		state = "running"
	}
	frames := perf.summary()
	cache := defaultRenderCache.stats()
	cacheLookups := cache.Hits + cache.Misses
	hitRate := "n/a"
	if cacheLookups > 0 {
		hitRate = fmt.Sprintf("%.1f%%", 100*float64(cache.Hits)/float64(cacheLookups))
	}
	return renderCommandOutput(commandOutput{
		Title:  "Debug",
		Status: commandStatusInfo,
		Sections: []commandSection{
			{
				Title: "Runtime",
				Lines: []string{
					"run state: " + state,
					"active run: " + fmt.Sprint(m.activeRunID),
					"pending permission: " + boolText(m.pendingPermission != nil),
				},
			},
			{
				Title: "Frames (last " + fmt.Sprint(perfRingSize) + ")",
				Lines: []string{
					"view: p50 " + formatDuration(frames.ViewP50) + " · p95 " + formatDuration(frames.ViewP95) + " · max " + formatDuration(frames.ViewMax) + " · mean " + formatDuration(frames.ViewMean),
					"update: p50 " + formatDuration(frames.UpdateP50) + " · p95 " + formatDuration(frames.UpdateP95) + " · max " + formatDuration(frames.UpdateMax) + " · mean " + formatDuration(frames.UpdateMean),
					"calls: view " + fmt.Sprint(frames.ViewCount) + " · update " + fmt.Sprint(frames.UpdateCount),
				},
			},
			{
				Title: "Frames by trigger (view p95 / max, worst first)",
				Lines: frameByTriggerLines(frames.ByTag),
			},
			{
				Title: "Render cache",
				Lines: []string{
					"hits: " + fmt.Sprint(cache.Hits) + " · misses: " + fmt.Sprint(cache.Misses) + " · hit rate: " + hitRate,
					"evictions: " + fmt.Sprint(cache.Evictions) + " · skipped oversized: " + fmt.Sprint(cache.SkippedOversized),
				},
			},
			{
				Title: "Transcript",
				Lines: []string{
					"rows: " + fmt.Sprint(len(m.transcript)) + " · flushed: " + fmt.Sprint(m.flushed),
					"alt screen: " + boolText(m.altScreen) + " · sidebar: " + boolText(m.sidebarActive()),
				},
			},
		},
	})
}

// frameByTriggerLines renders the per-trigger frame table for /debug. Each line
// is "<tag>: view p95 <d> · max <d> · n <count>"; update stats are omitted when
// zero so idle triggers do not add noise. The list is already sorted
// worst-view-p95-first by perfSummary.
func frameByTriggerLines(byTag []tagStat) []string {
	if len(byTag) == 0 {
		return []string{"(no frames recorded yet)"}
	}
	lines := make([]string, 0, len(byTag))
	for _, t := range byTag {
		line := t.Tag + ": view p95 " + formatDuration(t.ViewP95) + " · max " + formatDuration(t.ViewMax) + " · n " + fmt.Sprint(t.ViewCount)
		if t.UpdateCount > 0 {
			line += " · update p95 " + formatDuration(t.UpdateP95) + " · max " + formatDuration(t.UpdateMax)
		}
		lines = append(lines, line)
	}
	return lines
}

// skillsText is the /skills fallback when NO skills are installed — an install
// hint. With skills present /skills opens the searchable skill picker instead
// (see newSkillPicker), matching how /model works.
func (m model) skillsText() string {
	return renderCommandOutput(commandOutput{
		Title:  "Skills",
		Status: commandStatusInfo,
		Sections: []commandSection{{
			Lines: []string{"No skills installed."},
		}},
		Hints: []string{
			"install one: create <skills-dir>/<name>/SKILL.md (see `splice skills`)",
		},
	})
}
