package wiring

// Contracts is the wiring registry. Every entry names one seam that must stay
// connected. Add a contract when a feature gains a producer that another layer
// must consume, and name the test that proves the behavior end to end.
//
// Producer keys are module-relative: "dir.Func", "dir.Type", "dir.Type.Field",
// or "dir.Type.Method". Consumer keys are production functions or methods.
var Contracts = []Contract{
	// TUI v0.5: run health and the blocking gate reach the chrome through
	// one selector, so the status chip and the pipeline header agree.
	{
		ID:       "presentation.health.runtime_view",
		Producer: "internal/presentation.State.Health",
		Consumer: "internal/tui.model.runtimeView",
		Proof:    "internal/tui.TestRuntimeViewProjectsLiveGates",
	},
	{
		ID:       "presentation.gate.runtime_view",
		Producer: "internal/presentation.State.Gate",
		Consumer: "internal/tui.model.runtimeView",
		Proof:    "internal/tui.TestRuntimeViewProjectsLiveGates",
	},
	// Track T: the compiled dependency graph decides what each stage sees.
	{
		ID:       "topology.depends_on.scoping",
		Producer: "internal/splice/schemas.ExecutionStage.DependsOn",
		Consumer: "internal/splice.scopedStageInputs",
		Proof:    "internal/splice.TestScopedStageInputs",
	},
	{
		ID:       "topology.depends_on.compiled",
		Producer: "internal/splice/schemas.ExecutionStage.DependsOn",
		Consumer: "internal/splice.CompileTopology",
		Proof:    "internal/splice.TestDefaultTopologyDependsOn",
	},
	{
		ID:       "topology.depends_on.validated",
		Producer: "internal/splice/schemas.ExecutionStage.DependsOn",
		Consumer: "internal/splice/schemas.ExecutionPlan.Validate",
		Proof:    "internal/splice/schemas.TestExecutionPlanDependsOnIntegrity",
	},
	// Track T: compiled capabilities drive the executor.
	{
		ID:       "topology.caps.executor",
		Producer: "internal/splice/schemas.ExecutionStage.Caps",
		Consumer: "internal/splice.effectiveCaps",
		Proof:    "internal/splice.TestCompileTopologyCarriesResolvedCaps",
	},
	{
		ID:       "topology.caps.plan_verification",
		Producer: "internal/splice/schemas.ExecutionStage.Caps",
		Consumer: "internal/splice.planHasVerification",
		Proof:    "internal/splice.TestPlanHasVerification",
	},
	{
		ID:       "topology.type.capability_fallback",
		Producer: "internal/splice/schemas.ExecutionStage.Type",
		Consumer: "internal/splice.stageCapabilities",
		Proof:    "internal/splice.TestStageModelFreeFallsBackToBuiltinProfile",
	},
	// Track T: the node model ladder.
	{
		ID:       "topology.model.ladder",
		Producer: "internal/splice/schemas.ExecutionStage.Model",
		Consumer: "internal/splice.nodeModelOverride",
		Proof:    "internal/splice.TestCompileTopologyCarriesNodeModel",
	},
	// T8: the canonical verification key reaches the trajectory monitor.
	{
		ID:       "verification.canonical_key",
		Producer: "internal/splice.VerificationReportKey",
		Consumer: "internal/splice.normalizeVerificationReport",
		Proof:    "internal/splice.TestNormalizeVerificationReport",
	},
	{
		ID:       "verification.round_trip_decode",
		Producer: "internal/splice.VerificationReportKey",
		Consumer: "internal/splice.verificationReport",
		Proof:    "internal/splice.TestVerificationReportSurvivesJSONRoundTrip",
	},
	// M1: splice_min_version is enforced against the build version.
	{
		ID:       "topology.min_splice.gate",
		Producer: "internal/splice/schemas.PipelineTopology.MinSplice",
		Consumer: "internal/splice/schemas.PipelineTopology.ValidateMinSplice",
		Proof:    "internal/splice.TestResolveTopologyEnforcesMinSplice",
	},
	{
		ID:       "version.build_value.loader",
		Producer: "internal/version.Version",
		Consumer: "internal/splice.DecodeTopology",
		Proof:    "internal/splice.TestResolveTopologyEnforcesMinSplice",
	},
	// Additive changed files reach the orchestrator's file accounting.
	{
		ID:       "stage.changed_files",
		Producer: "internal/splice/schemas.HarnessStageOutput.ChangedFiles",
		Consumer: "internal/splice.stageChangedFiles",
		Proof:    "internal/splice.TestStageChangedFilesPrefersAdditiveField",
	},
	// T5: prompt context is rendered through the delimiter wrappers.
	{
		ID:       "stage.prompt_delimiters",
		Producer: "internal/splice/stages.renderPromptSummaries",
		Consumer: "internal/splice/stages.renderPromptTemplate",
		Proof:    "internal/splice/stages.TestPromptStageDelimitsUntrustedContext",
	},
	// Every stage model call goes through one request gate.
	{
		ID:       "stage.request_gate",
		Producer: "internal/splice/stages.streamCompletion",
		Consumer: "internal/splice/stages.callTextCompletion",
		Proof:    "internal/splice/stages.TestPromptStageRendersTemplateWithoutTools",
	},

	// Phase 1: the pipeline graph view reads the dependency graph and the
	// compiled tier.
	{
		ID:       "presentation.dependencies.graph_view",
		Producer: "internal/presentation.ExecutionNode.Dependencies",
		Consumer: "internal/splice/presentrun.BuildGraph",
		Proof:    "internal/splice/presentrun.TestBuildGraphDiamond",
	},
	{
		ID:       "topology.compiled_tier.warnings",
		Producer: "internal/splice.CompiledTopology.Tier",
		Consumer: "internal/splice.TopologyWarnings",
		Proof:    "internal/splice.TestTopologyWarningsNameTheirTiers",
	},

	// Deliberately unwired. Each entry records the owner that will consume the
	// producer. The checker fails if the symbol disappears.
}
