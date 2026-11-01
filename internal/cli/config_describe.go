package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/modelregistry"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// describeReport is the bounded `splice config describe` key set from the
// pipeline plan section 5: the active pipeline and its source layer, the
// effective token envelope and its origin, each node's resolved config with the
// origin of its model, and the stage-models.json entries in effect. Nothing
// outside this set is reported.
type describeReport struct {
	ActivePipeline string             `json:"activePipeline"`
	PipelineSource string             `json:"pipelineSource"`
	Workspace      string             `json:"workspace"`
	Trusted        bool               `json:"trusted"`
	Tier           string             `json:"tier"`
	Envelope       describeEnvelope   `json:"envelope"`
	Nodes          []describeNode     `json:"nodes"`
	StageModels    describeStageModel `json:"stageModels"`
}

type describeEnvelope struct {
	TotalInput  int    `json:"totalInput"`
	TotalOutput int    `json:"totalOutput"`
	Reserve     int    `json:"reserve"`
	Overflow    string `json:"overflowPolicy"`
	Origin      string `json:"origin"`
	// AgentInput and AgentOutput are the agent nodes' explicit caps. They are
	// added on top of TotalInput and TotalOutput, not drawn from them.
	AgentInput  int `json:"agentInput,omitempty"`
	AgentOutput int `json:"agentOutput,omitempty"`
}

type describeNode struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Tiers        []string `json:"tiers,omitempty"`
	ModelOrigin  string   `json:"modelOrigin"`
	ModelProfile string   `json:"modelProfile,omitempty"`
	Model        string   `json:"model,omitempty"`
	// ResolvedModel names the model the tier layer would pick when the origin
	// is "tier". It is empty for every other origin and when the provider
	// config cannot be read.
	ResolvedModel        string `json:"resolvedModel,omitempty"`
	Effort               string `json:"effort,omitempty"`
	BudgetInput          int    `json:"budgetInput"`
	BudgetOutput         int    `json:"budgetOutput"`
	ModelFree            bool   `json:"modelFree"`
	PullContext          bool   `json:"pullContext"`
	PullMemory           bool   `json:"pullMemory"`
	ProducesVerification bool   `json:"producesVerification"`
}

type describeStageModel struct {
	Path    string            `json:"path"`
	Default string            `json:"default,omitempty"`
	Stages  map[string]string `json:"stages,omitempty"`
}

// runConfigDescribe implements `splice config describe [--tier T] [--json]`.
func runConfigDescribe(args []string, stdout, stderr io.Writer, deps appDeps) int {
	tier := schemas.TierStandard
	asJSON := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--json":
			asJSON = true
		case arg == "--tier":
			if index+1 >= len(args) {
				return writeExecUsageError(stderr, "--tier requires a value")
			}
			index++
			parsed, err := parseDescribeTier(args[index])
			if err != nil {
				return writeExecUsageError(stderr, err.Error())
			}
			tier = parsed
		case strings.HasPrefix(arg, "--tier="):
			parsed, err := parseDescribeTier(strings.TrimPrefix(arg, "--tier="))
			if err != nil {
				return writeExecUsageError(stderr, err.Error())
			}
			tier = parsed
		default:
			return writeExecUsageError(stderr, fmt.Sprintf("unknown config describe argument %q", arg))
		}
	}

	report, err := buildDescribeReport(deps, tier)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitProvider)
	}
	if asJSON {
		if err := writePrettyJSON(stdout, report); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprint(stdout, formatDescribeReport(report)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func parseDescribeTier(value string) (schemas.PipelineTier, error) {
	tier := schemas.PipelineTier(strings.TrimSpace(value))
	if err := tier.Validate(); err != nil {
		return "", err
	}
	return tier, nil
}

func buildDescribeReport(deps appDeps, tier schemas.PipelineTier) (describeReport, error) {
	workspaceRoot, err := resolveWorkspaceRoot("", deps)
	if err != nil {
		return describeReport{}, err
	}
	trusted := workspaceTrusted(workspaceRoot)
	topology, source, _, err := splicerun.ResolveTopology(splicerun.TopologySourcesFor(workspaceRoot, trusted))
	if err != nil {
		return describeReport{}, err
	}
	stageConfig, stageConfigPath, err := loadDescribeStageModels(deps)
	if err != nil {
		return describeReport{}, err
	}
	compiled, nodes, err := splicerun.DescribeTopology(topology, tier, stageConfig)
	if err != nil {
		return describeReport{}, err
	}

	envelopeOrigin := "tier envelope"
	if topology.Budget != nil {
		envelopeOrigin = "topology budget.total"
	}
	report := describeReport{
		ActivePipeline: topology.Name,
		PipelineSource: source,
		Workspace:      workspaceRoot,
		Trusted:        trusted,
		Tier:           string(tier),
		Envelope: describeEnvelope{
			TotalInput:  compiled.TierInput,
			TotalOutput: compiled.TierOutput,
			Reserve:     compiled.Budget.Reserve,
			Overflow:    compiled.Budget.OverflowPolicy,
			Origin:      envelopeOrigin,
			AgentInput:  compiled.AgentInput,
			AgentOutput: compiled.AgentOutput,
		},
		StageModels: describeStageModel{Path: stageConfigPath, Stages: map[string]string{}},
	}
	if stageConfig.Default.ProviderProfile != "" || stageConfig.Default.Model != "" {
		report.StageModels.Default = formatModelConfig(stageConfig.Default)
	}
	for name, cfg := range stageConfig.Stages {
		report.StageModels.Stages[name] = formatModelConfig(cfg)
	}

	tierModels := describeTierModels(deps, workspaceRoot, nodes)
	for _, node := range nodes {
		entry := describeNode{
			Name:                 node.Name,
			ResolvedModel:        tierModels[node.Name],
			Type:                 node.Type,
			ModelOrigin:          node.ModelOrigin,
			ModelProfile:         node.Model.ProviderProfile,
			Model:                node.Model.Model,
			Effort:               node.Model.ReasoningEffort,
			BudgetInput:          node.Budget.InputMax,
			BudgetOutput:         node.Budget.OutputMax,
			ModelFree:            node.Caps.ModelFree,
			PullContext:          node.Caps.PullContext != nil && *node.Caps.PullContext,
			PullMemory:           node.Caps.PullMemory != nil && *node.Caps.PullMemory,
			ProducesVerification: node.Caps.ProducesVerification,
		}
		for _, nodeTier := range node.Tiers {
			entry.Tiers = append(entry.Tiers, string(nodeTier))
		}
		report.Nodes = append(report.Nodes, entry)
	}
	return report, nil
}

// describeTierModels resolves the tier layer for each node that names it. A
// missing or unreadable provider config leaves the map empty, so describe still
// reports the topology without a provider.
func describeTierModels(deps appDeps, workspaceRoot string, nodes []splicerun.NodeDescription) map[string]string {
	out := make(map[string]string)
	if deps.resolveConfig == nil {
		return out
	}
	resolved, err := deps.resolveConfig(workspaceRoot, config.Overrides{})
	if err != nil {
		return out
	}
	registry, err := modelregistry.DefaultRegistry(resolved.Provider.Name)
	if err != nil {
		return out
	}
	for _, node := range nodes {
		if node.ModelOrigin != splicerun.ModelOriginTier {
			continue
		}
		entry := splicerun.ResolveStageTierModel(node.Name, resolved.Provider, registry)
		if entry != nil {
			out[node.Name] = entry.ID
		}
	}
	return out
}

func loadDescribeStageModels(deps appDeps) (schemas.StageModelConfigFile, string, error) {
	userConfigPath, err := deps.userConfigPath()
	if err != nil {
		return schemas.StageModelConfigFile{}, "", err
	}
	path := filepath.Join(filepath.Dir(userConfigPath), "stage-models.json")
	cfg, err := schemas.LoadStageModelConfig(path)
	if err != nil {
		return schemas.StageModelConfigFile{}, path, err
	}
	return cfg, path, nil
}

func formatModelConfig(cfg schemas.StageModelConfig) string {
	value := cfg.ProviderProfile + "/" + cfg.Model
	if cfg.ReasoningEffort != "" {
		value += " (" + cfg.ReasoningEffort + ")"
	}
	return value
}

func formatDescribeReport(report describeReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "active pipeline: %s (%s)\n", report.ActivePipeline, report.PipelineSource)
	fmt.Fprintf(&b, "workspace: %s (%s)\n", report.Workspace, trustLabel(report.Trusted))
	fmt.Fprintf(&b, "envelope (%s): input=%d output=%d reserve=%d overflow=%s (origin: %s)\n",
		report.Tier, report.Envelope.TotalInput, report.Envelope.TotalOutput, report.Envelope.Reserve, report.Envelope.Overflow, report.Envelope.Origin)
	if report.Envelope.AgentInput > 0 || report.Envelope.AgentOutput > 0 {
		fmt.Fprintf(&b, "+ agent nodes: input=%d output=%d (added on top of the envelope)\n", report.Envelope.AgentInput, report.Envelope.AgentOutput)
	}
	fmt.Fprintf(&b, "nodes (%s):\n", report.Tier)
	for _, node := range report.Nodes {
		model := "primary"
		if node.Model != "" {
			model = node.ModelProfile + "/" + node.Model
			if node.Effort != "" {
				model += " (" + node.Effort + ")"
			}
		} else if node.ModelOrigin == splicerun.ModelOriginTier && node.ResolvedModel != "" {
			model = node.ResolvedModel + " (tier)"
		}
		tiers := "all tiers"
		if len(node.Tiers) > 0 {
			tiers = strings.Join(node.Tiers, ",")
		}
		fmt.Fprintf(&b, "  %s type=%s tiers=%s\n", node.Name, node.Type, tiers)
		fmt.Fprintf(&b, "    model=%s origin=%s\n", model, node.ModelOrigin)
		fmt.Fprintf(&b, "    budget=input:%d output:%d\n", node.BudgetInput, node.BudgetOutput)
		fmt.Fprintf(&b, "    caps=model_free:%t pull_context:%t pull_memory:%t produces_verification:%t\n",
			node.ModelFree, node.PullContext, node.PullMemory, node.ProducesVerification)
	}
	fmt.Fprintf(&b, "stage-models.json: %s\n", report.StageModels.Path)
	if report.StageModels.Default != "" {
		fmt.Fprintf(&b, "  default: %s\n", report.StageModels.Default)
	}
	names := make([]string, 0, len(report.StageModels.Stages))
	for name := range report.StageModels.Stages {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(&b, "  %s: %s\n", name, report.StageModels.Stages[name])
	}
	return b.String()
}

func trustLabel(trusted bool) string {
	if trusted {
		return "trusted"
	}
	return "untrusted"
}
