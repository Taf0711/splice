package splice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/modelregistry"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/zeroruntime"
)

type routingTestProvider struct {
	model string
}

func (*routingTestProvider) StreamCompletion(context.Context, zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	return nil, nil
}

func TestBuildStageModelResolvers(t *testing.T) {
	configFile := schemas.StageModelConfigFile{
		Default:    schemas.StageModelConfig{ProviderProfile: "local", Model: "qwen-default", ReasoningEffort: "low"},
		Escalation: &schemas.StageModelConfig{ProviderProfile: "cloud", Model: "cloud-large", ReasoningEffort: "high"},
		Stages: map[string]schemas.StageModelConfig{
			"code_writer": {ProviderProfile: "local", Model: "qwen-coder", ReasoningEffort: "medium"},
		},
	}
	profiles := []config.ProviderProfile{{Name: "local", Model: "old-local"}, {Name: "cloud", Model: "old-cloud"}}
	builds := map[string]int{}
	stageResolver, escalationResolver := testModelResolvers(configFile, profiles, func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
		builds[profile.Name+"/"+profile.Model]++
		return &routingTestProvider{model: profile.Model}, nil
	}, TierResolverConfig{})

	selection, err := stageResolver("code_writer")
	if err != nil {
		t.Fatal(err)
	}
	if got := selection.Provider.(*routingTestProvider).model; got != "qwen-coder" || selection.ProviderName != "local" || selection.Model != "qwen-coder" || selection.ReasoningEffort != "medium" {
		t.Fatalf("specific route = %+v, provider model %q", selection, got)
	}

	defaultSelection, err := stageResolver("test_generator")
	if err != nil {
		t.Fatal(err)
	}
	if got := defaultSelection.Provider.(*routingTestProvider).model; got != "qwen-default" || defaultSelection.ProviderName != "local" || defaultSelection.Model != "qwen-default" || defaultSelection.ReasoningEffort != "low" {
		t.Fatalf("default route = %+v, provider model %q", defaultSelection, got)
	}
	cachedSelection, err := stageResolver("static_analyzer")
	if err != nil {
		t.Fatal(err)
	}
	if cachedSelection.Provider != defaultSelection.Provider || builds["local/qwen-default"] != 1 {
		t.Fatalf("default provider was not cached: builds=%v", builds)
	}

	escalation, err := escalationResolver()
	if err != nil {
		t.Fatal(err)
	}
	if got := escalation.Provider.(*routingTestProvider).model; got != "cloud-large" || escalation.ProviderName != "cloud" || escalation.Model != "cloud-large" || escalation.ReasoningEffort != "high" {
		t.Fatalf("escalation route = %+v, provider model %q", escalation, got)
	}
}

func TestBuildStageModelResolversAbsentConfigIsNoOp(t *testing.T) {
	stageResolver, escalationResolver := testModelResolvers(schemas.StageModelConfigFile{}, nil, nil, TierResolverConfig{})
	selection, err := stageResolver("code_writer")
	if err != nil || selection != (agent.ModelSelection{}) {
		t.Fatalf("absent stage config = (%+v, %v), want no-op", selection, err)
	}
	selection, err = escalationResolver()
	if err != nil || selection != (agent.ModelSelection{}) {
		t.Fatalf("absent escalation = (%+v, %v), want no-op", selection, err)
	}
}

func TestBuildStageModelResolversErrorsNameRoute(t *testing.T) {
	configFile := schemas.StageModelConfigFile{
		Default: schemas.StageModelConfig{ProviderProfile: "missing", Model: "model"},
		Stages: map[string]schemas.StageModelConfig{
			"code_writer": {ProviderProfile: "broken", Model: "model"},
		},
	}
	stageResolver, _ := testModelResolvers(configFile, []config.ProviderProfile{{Name: "broken"}}, func(config.ProviderProfile) (zeroruntime.Provider, error) {
		return nil, errors.New("factory failed")
	}, TierResolverConfig{})
	if _, err := stageResolver("test_generator"); err == nil || !strings.Contains(err.Error(), `stage "test_generator" references unknown provider profile "missing"`) {
		t.Fatalf("unknown-profile error = %v", err)
	}
	if _, err := stageResolver("code_writer"); err == nil || !strings.Contains(err.Error(), `build provider for stage "code_writer": factory failed`) {
		t.Fatalf("factory error = %v", err)
	}
}

// P3-7: a stage-models.json route whose model the profile's endpoint family
// cannot serve names stage-models.json and the fix, and it is invalid input,
// not a provider failure.
func TestStageModelRouteEndpointFamilyErrorNamesTheFix(t *testing.T) {
	configFile := schemas.StageModelConfigFile{
		Escalation: &schemas.StageModelConfig{ProviderProfile: "openai", Model: "qwen3-coder-30b"},
		Stages: map[string]schemas.StageModelConfig{
			"code_writer": {ProviderProfile: "openai", Model: "qwen3-coder-30b"},
		},
	}
	stageResolver, escalationResolver := testModelResolvers(configFile, []config.ProviderProfile{{Name: "openai"}}, func(config.ProviderProfile) (zeroruntime.Provider, error) {
		return nil, &modelregistry.EndpointFamilyError{Model: "qwen/qwen3-coder-30b-a3b-instruct", Family: "openai"}
	}, TierResolverConfig{})
	_, stageErr := stageResolver("code_writer")
	_, escalationErr := escalationResolver()
	for name, err := range map[string]error{"stage": stageErr, "escalation": escalationErr} {
		if err == nil || !strings.Contains(err.Error(), "is not served by the openai endpoint family") ||
			!strings.Contains(err.Error(), "edit stage-models.json or re-run onboarding (splice setup)") {
			t.Fatalf("%s error = %v, want the endpoint-family refusal naming stage-models.json and the fix", name, err)
		}
		var build *providerBuildError
		if errors.As(err, &build) {
			t.Fatalf("%s error is a provider build failure; an unservable route is invalid input", name)
		}
	}
}

func TestBuildStageModelResolversExplicitOverrideWinsOverTier(t *testing.T) {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	configFile := schemas.StageModelConfigFile{
		Stages: map[string]schemas.StageModelConfig{
			"code_writer": {ProviderProfile: "primary", Model: "explicit-model", ReasoningEffort: "medium"},
		},
	}
	primaryProfile := config.ProviderProfile{Name: "primary", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-5.6-sol"}
	stageResolver, _ := testModelResolvers(
		configFile,
		[]config.ProviderProfile{primaryProfile},
		func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			return &routingTestProvider{model: profile.Model}, nil
		},
		TierResolverConfig{PrimaryProfile: primaryProfile, Registry: &registry},
	)
	selection, err := stageResolver("code_writer")
	if err != nil {
		t.Fatalf("stageResolver: %v", err)
	}
	if selection.Model != "explicit-model" || selection.ProviderName != "primary" {
		t.Fatalf("selection = %+v, want explicit primary route", selection)
	}
	if got := selection.Provider.(*routingTestProvider).model; got != "explicit-model" {
		t.Fatalf("provider model = %q, want explicit-model", got)
	}
}

func TestBuildStageModelResolversTierFallbackUsedWhenNoOverride(t *testing.T) {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	primaryProfile := config.ProviderProfile{Name: "primary", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-5.6-sol"}
	stageResolver, _ := testModelResolvers(
		schemas.StageModelConfigFile{},
		[]config.ProviderProfile{primaryProfile},
		func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			return &routingTestProvider{model: profile.Model}, nil
		},
		TierResolverConfig{PrimaryProfile: primaryProfile, Registry: &registry},
	)
	selection, err := stageResolver("code_writer")
	if err != nil {
		t.Fatalf("stageResolver: %v", err)
	}
	if selection.Provider == nil || selection.Model == "" || selection.ProviderName != "primary" {
		t.Fatalf("expected tier fallback, got %+v", selection)
	}
	if selection.Model != "gpt-4.1-nano" {
		t.Fatalf("tier fallback model = %q, want gpt-4.1-nano", selection.Model)
	}
}

func TestBuildStageModelResolversNoTierLabelFallsBackToPrimary(t *testing.T) {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	primaryProfile := config.ProviderProfile{Name: "primary", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-5.6-sol"}
	stageResolver, _ := testModelResolvers(
		schemas.StageModelConfigFile{},
		[]config.ProviderProfile{primaryProfile},
		func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			return &routingTestProvider{model: profile.Model}, nil
		},
		TierResolverConfig{PrimaryProfile: primaryProfile, Registry: &registry},
	)
	selection, err := stageResolver("static_analyzer")
	if err != nil {
		t.Fatalf("stageResolver: %v", err)
	}
	if selection.Provider != nil || selection.Model != "" {
		t.Fatalf("deterministic stage = %+v, want zero selection", selection)
	}
}

// testModelResolvers adapts the struct return to the tuple these routing tests
// destructure.
func testModelResolvers(
	configFile schemas.StageModelConfigFile,
	profiles []config.ProviderProfile,
	newProvider func(config.ProviderProfile) (zeroruntime.Provider, error),
	tierResolverConfig TierResolverConfig,
) (agent.StageModelResolver, agent.EscalationModelResolver) {
	built := BuildStageModelResolvers(configFile, profiles, newProvider, tierResolverConfig)
	return built.Stage, built.Escalation
}

// TestBuildNodeModelResolver pins the strongest rung: a node's explicit model
// resolves through the same build path, names the node on error, and shares the
// provider cache with a stage override of the same model.
func TestBuildNodeModelResolver(t *testing.T) {
	profiles := []config.ProviderProfile{{Name: "local", Model: "old-local"}}
	builds := map[string]int{}
	built := BuildStageModelResolvers(schemas.StageModelConfigFile{}, profiles, func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
		builds[profile.Name+"/"+profile.Model]++
		return &routingTestProvider{model: profile.Model}, nil
	}, TierResolverConfig{})

	selection, err := built.Node("summarizer", agent.ModelOverride{ProviderProfile: "local", Model: "qwen-node", ReasoningEffort: "high"})
	if err != nil {
		t.Fatalf("node resolver: %v", err)
	}
	if selection.ProviderName != "local" || selection.Model != "qwen-node" || selection.ReasoningEffort != "high" {
		t.Fatalf("node selection = %+v", selection)
	}
	if got := selection.Provider.(*routingTestProvider).model; got != "qwen-node" {
		t.Fatalf("node provider model = %q, want qwen-node", got)
	}

	again, err := built.Node("other", agent.ModelOverride{ProviderProfile: "local", Model: "qwen-node", ReasoningEffort: "high"})
	if err != nil {
		t.Fatalf("node resolver (cached): %v", err)
	}
	if again.Provider != selection.Provider || builds["local/qwen-node"] != 1 {
		t.Fatalf("node provider not cached: builds=%v", builds)
	}

	if _, err := built.Node("summarizer", agent.ModelOverride{ProviderProfile: "ghost", Model: "x"}); err == nil ||
		!strings.Contains(err.Error(), `node "summarizer" references unknown provider profile "ghost"`) {
		t.Fatalf("unknown profile error = %v", err)
	}
}

// TestResolveModelRoute pins the origin labels against the executor
// precedence, including the incomplete stage entry that the executor accepts
// and then fails to build.
func TestResolveModelRoute(t *testing.T) {
	nodeModel := &schemas.StageModelConfig{ProviderProfile: "team", Model: "node-model", ReasoningEffort: "high"}
	stageConfig := schemas.StageModelConfigFile{
		Default: schemas.StageModelConfig{ProviderProfile: "fallback", Model: "fallback-model"},
		Stages: map[string]schemas.StageModelConfig{
			"code_writer": {ProviderProfile: "local", Model: "stage-model"},
			"broken":      {},
		},
	}
	cases := []struct {
		name         string
		stage        string
		node         *schemas.StageModelConfig
		config       schemas.StageModelConfigFile
		hasTierLabel bool
		wantOrigin   string
		wantProfile  string
		wantModel    string
	}{
		{"node wins over everything", "code_writer", nodeModel, stageConfig, true, ModelOriginNode, "team", "node-model"},
		{"stage entry", "code_writer", nil, stageConfig, true, ModelOriginStage, "local", "stage-model"},
		{"incomplete stage entry still wins", "broken", nil, stageConfig, true, ModelOriginStage, "", ""},
		{"default entry", "test_generator", nil, stageConfig, true, ModelOriginDefault, "fallback", "fallback-model"},
		{"tier label", "test_generator", nil, schemas.StageModelConfigFile{}, true, ModelOriginTier, "", ""},
		{"primary", "test_generator", nil, schemas.StageModelConfigFile{}, false, ModelOriginPrimary, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, origin := ResolveModelRoute(tc.stage, tc.node, tc.config, tc.hasTierLabel)
			if origin != tc.wantOrigin {
				t.Fatalf("origin = %q, want %q", origin, tc.wantOrigin)
			}
			if cfg.ProviderProfile != tc.wantProfile || cfg.Model != tc.wantModel {
				t.Fatalf("config = %+v, want %s/%s", cfg, tc.wantProfile, tc.wantModel)
			}
		})
	}
}

// A stage routed to its own model without an effort of its own inherits the
// run's effort (it used to send none), fitted to that model's levels.
func TestStageReasoningEffortInheritsAndFits(t *testing.T) {
	modelregistry.EnableModelsDevOverlay()
	reg, err := modelregistry.DefaultRegistry("openrouter")
	if err != nil {
		t.Fatal(err)
	}
	options := PipelineRunConfig{Model: "anthropic/claude-opus-5", ReasoningEffort: "medium", ModelRegistry: reg}
	cases := []struct {
		name      string
		selection agent.ModelSelection
		want      string
	}{
		{"primary", agent.ModelSelection{Model: "anthropic/claude-opus-5", ReasoningEffort: "medium"}, "medium"},
		{"routed stage inherits and fits", agent.ModelSelection{Model: "z-ai/glm-5.2"}, "high"},
		{"stage keeps its own effort", agent.ModelSelection{Model: "anthropic/claude-opus-5", ReasoningEffort: "max"}, "max"},
		{"unknown model passes through", agent.ModelSelection{Model: "my-proxy/custom"}, "medium"},
	}
	for _, tc := range cases {
		if got := stageReasoningEffort(options, tc.selection); got != tc.want {
			t.Fatalf("%s: effort = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// PinProfileGaps names each pin whose profile the config does not define, and
// nothing else.
func TestPinProfileGaps(t *testing.T) {
	topology := &schemas.PipelineTopology{Nodes: []schemas.PipelineNode{
		{Name: "code_writer", Type: "code_writer", Model: &schemas.StageModelConfig{ProviderProfile: "team", Model: "m"}},
		{Name: "test_generator", Type: "test_generator", Model: &schemas.StageModelConfig{ProviderProfile: "ghost", Model: "m"}},
		{Name: "lint", Type: schemas.NodeTypeCommand, Command: []string{"true"}},
	}}
	gaps := PinProfileGaps(topology, []config.ProviderProfile{{Name: "team"}})
	if len(gaps) != 1 || !strings.Contains(gaps[0], "test_generator") || !strings.Contains(gaps[0], `"ghost"`) {
		t.Fatalf("gaps = %v, want one gap naming test_generator and ghost", gaps)
	}
	if got := PinProfileGaps(topology, []config.ProviderProfile{{Name: "team"}, {Name: "ghost"}}); len(got) != 0 {
		t.Fatalf("gaps = %v, want none when every profile exists", got)
	}
}

// TestStageRouteFailureFailsRun pins D2: a stage-models.json route that cannot
// resolve fails the run, names the stage, and sends no request on that route.
// A route that is absent keeps the run model. A tier-layer failure keeps the
// run model with a warning.
func TestStageRouteFailureFailsRun(t *testing.T) {
	plan := func() schemas.ExecutionPlan {
		return schemas.ExecutionPlan{Tier: schemas.TierStandard, RequestIntent: "stage route", Stages: []schemas.ExecutionStage{{Name: "code_writer"}}}
	}
	var notices []string
	run := func(resolver func(string) (agent.ModelSelection, error)) (map[string]capturedStageCall, error) {
		capturer := &stageCallCapturer{calls: map[string]capturedStageCall{}}
		notices = nil
		options := PipelineConfigFromAgentOptions(agent.Options{StageModelResolver: resolver})
		options.OnNotice = func(text string) { notices = append(notices, text) }
		_, _, _, err := runPass(context.Background(), "run-stage-route", 1, plan(), stageRegistry{"code_writer": capturer}, &namedProvider{name: "default"}, options, t.TempDir(), nil, time.Time{}, nil, nil, nil, nil)
		return capturer.calls, err
	}

	t.Run("broken route fails the run before the stage runs", func(t *testing.T) {
		calls, err := run(func(string) (agent.ModelSelection, error) {
			return agent.ModelSelection{}, errors.New(`stage "code_writer" references unknown provider profile "nosuch"`)
		})
		if err == nil || !strings.Contains(err.Error(), "stage code_writer model") || !strings.Contains(err.Error(), "nosuch") {
			t.Fatalf("err = %v, want a route failure naming the stage and the profile", err)
		}
		if _, ran := calls["code_writer"]; ran {
			t.Fatal("the stage ran on a broken route, want zero requests")
		}
	})
	t.Run("absent route keeps the run model", func(t *testing.T) {
		calls, err := run(func(string) (agent.ModelSelection, error) {
			return agent.ModelSelection{}, nil
		})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if _, ran := calls["code_writer"]; !ran {
			t.Fatal("the stage did not run on the run model")
		}
	})
	t.Run("tier-layer failure keeps the run model", func(t *testing.T) {
		calls, err := run(func(string) (agent.ModelSelection, error) {
			return agent.ModelSelection{}, &stageTierFallback{err: errors.New("build provider for tier model: factory failed")}
		})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if _, ran := calls["code_writer"]; !ran {
			t.Fatal("the stage did not run on the run model after a tier failure")
		}
		// The fallback is a visible notice, not a progress line.
		if len(notices) != 1 || !strings.Contains(notices[0], "tier route failed, using the run model") {
			t.Fatalf("notices = %q, want the tier fallback notice", notices)
		}
	})
}

// TestRepairStageRouteFailureSurfaces pins that repair re-entry follows the
// same rule as the pass loop: a broken stage route is an error naming the
// stage, and a tier-layer failure keeps the run model.
func TestRepairStageRouteFailureSurfaces(t *testing.T) {
	routeErr := errors.New("profile team missing")
	_, err := repairSelection(PipelineRunConfig{
		StageModelResolver: func(string) (agent.ModelSelection, error) {
			return agent.ModelSelection{}, routeErr
		},
	}, &namedProvider{name: "default"}, "code_writer", nil, false)
	if err == nil || !strings.Contains(err.Error(), "stage code_writer model") || !errors.Is(err, routeErr) {
		t.Fatalf("err = %v, want the stage route error naming code_writer", err)
	}

	sel, err := repairSelection(PipelineRunConfig{
		StageModelResolver: func(string) (agent.ModelSelection, error) {
			return agent.ModelSelection{}, &stageTierFallback{err: routeErr}
		},
	}, &namedProvider{name: "default"}, "code_writer", nil, false)
	if err != nil || sel.Provider == nil {
		t.Fatalf("tier failure: sel=%+v err=%v, want the run model without error", sel, err)
	}
}

// TestBuildStageModelResolversMarksTierFailureAsFallback pins the D2 tier
// layer at its source: when the automatic tier route cannot build its
// provider, the resolver returns a stageTierFallback, so the run warns and
// keeps the run model. A plain error here would fail the run instead.
func TestBuildStageModelResolversMarksTierFailureAsFallback(t *testing.T) {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	primaryProfile := config.ProviderProfile{Name: "primary", ProviderKind: config.ProviderKindOpenAI, Model: "gpt-5.6-sol"}
	factoryErr := errors.New("factory failed")
	stageResolver, _ := testModelResolvers(
		schemas.StageModelConfigFile{},
		[]config.ProviderProfile{primaryProfile},
		func(config.ProviderProfile) (zeroruntime.Provider, error) { return nil, factoryErr },
		TierResolverConfig{PrimaryProfile: primaryProfile, Registry: &registry},
	)
	_, err = stageResolver("code_writer")
	var fallback *stageTierFallback
	if !errors.As(err, &fallback) || !errors.Is(err, factoryErr) {
		t.Fatalf("err = %v, want a stageTierFallback that wraps the factory error", err)
	}
}

// TestRunRouteProfilesNamesEveryRoute: the primary, the decision advisor
// profile, each stage-models.json route, and each node pin are routes. A profile that only exists in the
// config is not. A stage model file that does not load makes the routes
// unknown.
func TestRunRouteProfilesNamesEveryRoute(t *testing.T) {
	dir := t.TempDir()
	stagePath := filepath.Join(dir, "stage-models.json")
	stageJSON := `{"default":{"provider_profile":"fallback","model":"m"},"escalation":{"provider_profile":"escalate","model":"m"},"stages":{"test_generator":{"provider_profile":"tests","model":"m"}}}`
	if err := os.WriteFile(stagePath, []byte(stageJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	topology := schemas.PipelineTopology{Version: schemas.TopologySchemaVersion, Name: "pinned", Nodes: []schemas.PipelineNode{
		{Name: "code_writer", Type: "code_writer", Model: &schemas.StageModelConfig{ProviderProfile: "pin", Model: "m"}},
	}}
	data, err := json.Marshal(topology)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(workspace, ".splice"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".splice", "pipeline.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	sources := TopologySources{WorkspaceRoot: workspace, Trusted: true, UserConfigDir: dir}
	names, known := RunRouteProfiles("primary", "classifier", stagePath, sources)
	if !known {
		t.Fatal("routes must be known when the stage file and the topology load")
	}
	for _, want := range []string{"primary", "classifier", "fallback", "escalate", "tests", "pin"} {
		if !names[want] {
			t.Fatalf("routes = %v, want %q", names, want)
		}
	}
	if names["unused"] || len(names) != 6 {
		t.Fatalf("routes = %v, want exactly the six routed profiles", names)
	}
	if err := os.WriteFile(stagePath, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, known := RunRouteProfiles("primary", "classifier", stagePath, sources); known {
		t.Fatal("an unreadable stage model file must make the routes unknown")
	}
}
