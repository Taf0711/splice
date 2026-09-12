package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

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
}

type describeNode struct {
	Name                 string   `json:"name"`
	Type                 string   `json:"type"`
	Tiers                []string `json:"tiers,omitempty"`
	ModelOrigin          string   `json:"modelOrigin"`
	ModelProfile         string   `json:"modelProfile,omitempty"`
	Model                string   `json:"model,omitempty"`
	Effort               string   `json:"effort,omitempty"`
	BudgetInput          int      `json:"budgetInput"`
	BudgetOutput         int      `json:"budgetOutput"`
	ModelFree            bool     `json:"modelFree"`
	PullContext          bool     `json:"pullContext"`
	PullMemory           bool     `json:"pullMemory"`
	ProducesVerification bool     `json:"producesVerification"`
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
	compiled, err := splicerun.CompileTopology(topology, tier)
	if err != nil {
		return describeReport{}, err
	}
	stageConfig, stageConfigPath, err := loadDescribeStageModels(deps)
	if err != nil {
		return describeReport{}, err
	}
	labels := splicerun.StageTierLabelsFor(topology)

	nodesByName := make(map[string]schemas.PipelineNode, len(topology.Nodes))
	for _, node := range topology.Nodes {
		nodesByName[node.Name] = node
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
			TotalInput:  compiled.Budget.TotalInputBudget,
			TotalOutput: compiled.Budget.TotalOutputBudget,
			Reserve:     compiled.Budget.Reserve,
			Overflow:    compiled.Budget.OverflowPolicy,
			Origin:      envelopeOrigin,
		},
		StageModels: describeStageModel{Path: stageConfigPath, Stages: map[string]string{}},
	}
	if stageConfig.Default.ProviderProfile != "" || stageConfig.Default.Model != "" {
		report.StageModels.Default = formatModelConfig(stageConfig.Default)
	}
	for name, cfg := range stageConfig.Stages {
		report.StageModels.Stages[name] = formatModelConfig(cfg)
	}

	for _, stage := range compiled.Stages {
		node := nodesByName[stage.Name]
		caps := schemas.NodeCapabilities{}
		if stage.Caps != nil {
			caps = *stage.Caps
		}
		modelCfg, origin := schemas.StageModelConfig{}, ""
		if caps.ModelFree {
			// A model-free node never resolves a model, so reporting a route
			// would be noise.
			origin = "model-free"
		} else {
			modelCfg, origin = splicerun.ResolveModelRoute(stage.Name, stage.Model, stageConfig, labels[stage.Name] != "")
		}
		entry := describeNode{
			Name:                 stage.Name,
			Type:                 node.Type,
			ModelOrigin:          origin,
			ModelProfile:         modelCfg.ProviderProfile,
			Model:                modelCfg.Model,
			Effort:               modelCfg.ReasoningEffort,
			BudgetInput:          stage.Budget.InputMax,
			BudgetOutput:         stage.Budget.OutputMax,
			ModelFree:            caps.ModelFree,
			PullContext:          caps.PullContext != nil && *caps.PullContext,
			PullMemory:           caps.PullMemory != nil && *caps.PullMemory,
			ProducesVerification: caps.ProducesVerification,
		}
		for _, nodeTier := range node.Tiers {
			entry.Tiers = append(entry.Tiers, string(nodeTier))
		}
		report.Nodes = append(report.Nodes, entry)
	}
	return report, nil
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
	fmt.Fprintf(&b, "nodes (%s):\n", report.Tier)
	for _, node := range report.Nodes {
		model := "primary"
		if node.Model != "" {
			model = node.ModelProfile + "/" + node.Model
			if node.Effort != "" {
				model += " (" + node.Effort + ")"
			}
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
