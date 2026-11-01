package v2

import "fmt"

// StageRoute is one resolved per-stage provider route. Routes are captured
// from the running system, not copied from configuration, so a mismatch
// invalidates the run before inference.
type StageRoute struct {
	Stage           string `json:"stage"`
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// Validate checks the route record.
func (r StageRoute) Validate() error {
	if r.Stage == "" || r.Provider == "" || r.Model == "" {
		return fmt.Errorf("stage route needs stage, provider, and model: %+v", r)
	}
	return nil
}

// NamedHash pairs an artifact name with its sha256 digest.
type NamedHash struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// Validate checks the pair.
func (h NamedHash) Validate() error {
	if h.Name == "" {
		return fmt.Errorf("hash name is required")
	}
	if !validHash(h.SHA256) {
		return fmt.Errorf("hash %s must be a sha256 hex digest, got %q", h.Name, h.SHA256)
	}
	return nil
}

// ToolchainVersion names one toolchain component version.
type ToolchainVersion struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// SidecarIdentity locks the memory sidecar build for the run.
type SidecarIdentity struct {
	Commit       string   `json:"commit"`
	BinarySHA256 string   `json:"binary_sha256"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}

// Manifest is the reproducibility manifest locked before the first provider
// call (protocol section 11). Locked manifests are never edited; a changed
// value requires a written amendment and a new experiment ID.
type Manifest struct {
	Protocol Protocol `json:"protocol"`

	// Build identity.
	SourceCommit   string   `json:"source_commit"`
	SourceClean    bool     `json:"source_clean"`
	BinarySHA256   string   `json:"binary_sha256"`
	ProtocolHash   string   `json:"protocol_hash"`
	AmendmentChain []string `json:"amendment_chain,omitempty"`

	// Environment identity.
	OS                string             `json:"os"`
	Arch              string             `json:"arch"`
	HardwareLabel     string             `json:"hardware_label"`
	ToolchainVersions []ToolchainVersion `json:"toolchain_versions"`
	Sidecar           SidecarIdentity    `json:"sidecar"`

	// Resolved routes, captured from the running system.
	ProviderProfile string       `json:"provider_profile"`
	StageRoutes     []StageRoute `json:"stage_routes"`

	// Artifact hashes.
	TaskHashes             []NamedHash `json:"task_hashes"`
	FixtureSHA256          string      `json:"fixture_sha256"`
	SnapshotSHA256         string      `json:"snapshot_sha256"`
	SelectionAuditSHA256   string      `json:"selection_audit_sha256"`
	CorpusProvenanceSHA256 string      `json:"corpus_provenance_sha256"`
	PromptSchemaHash       string      `json:"prompt_schema_hash"`
	ToolHash               string      `json:"tool_hash"`
	TopologyHash           string      `json:"topology_hash"`
	CompactionHash         string      `json:"compaction_hash"`
	BudgetHash             string      `json:"budget_hash"`
	AnalysisCodeHash       string      `json:"analysis_code_hash"`
	RuleHashes             []NamedHash `json:"rule_hashes"`

	// Sample and schedule.
	Tasks      []TaskSpec `json:"tasks"`
	Schedule   Schedule   `json:"schedule"`
	SampleSize int        `json:"sample_size"`

	// Spend.
	ExpectedCalls    int     `json:"expected_calls"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
}

// Validate checks structural integrity without lock requirements. Use it on
// drafts while assembling a manifest.
func (m Manifest) Validate() error {
	if err := m.Protocol.Validate(); err != nil {
		return fmt.Errorf("protocol: %w", err)
	}
	seenRoutes := make(map[string]bool, len(m.StageRoutes))
	for i, route := range m.StageRoutes {
		if err := route.Validate(); err != nil {
			return fmt.Errorf("stage_routes[%d]: %w", i, err)
		}
		if seenRoutes[route.Stage] {
			return fmt.Errorf("duplicate stage route %q", route.Stage)
		}
		seenRoutes[route.Stage] = true
	}
	for i, task := range m.Tasks {
		if err := task.Validate(); err != nil {
			return fmt.Errorf("tasks[%d]: %w", i, err)
		}
	}
	if err := m.Schedule.Validate(); err != nil {
		return fmt.Errorf("schedule: %w", err)
	}
	if len(m.Tasks) == 0 {
		return fmt.Errorf("tasks are required")
	}
	return nil
}

// ValidateLocked additionally enforces every lock requirement from protocol
// section 11: clean tree, full hashes, sidecar capabilities, route coverage,
// schedule completeness against the embedded protocol and task set, sample
// size consistency, and a positive spend cap.
func (m Manifest) ValidateLocked() error {
	if err := m.Validate(); err != nil {
		return err
	}
	if !m.SourceClean {
		return fmt.Errorf("locked manifest requires a clean source tree")
	}
	hashes := []struct{ name, value string }{
		{"binary_sha256", m.BinarySHA256},
		{"protocol_hash", m.ProtocolHash},
		{"fixture_sha256", m.FixtureSHA256},
		{"snapshot_sha256", m.SnapshotSHA256},
		{"selection_audit_sha256", m.SelectionAuditSHA256},
		{"corpus_provenance_sha256", m.CorpusProvenanceSHA256},
		{"prompt_schema_hash", m.PromptSchemaHash},
		{"tool_hash", m.ToolHash},
		{"topology_hash", m.TopologyHash},
		{"compaction_hash", m.CompactionHash},
		{"budget_hash", m.BudgetHash},
		{"analysis_code_hash", m.AnalysisCodeHash},
	}
	for _, h := range hashes {
		if !validHash(h.value) {
			return fmt.Errorf("%s must be a sha256 hex digest, got %q", h.name, h.value)
		}
	}
	if m.SourceCommit == "" {
		return fmt.Errorf("source_commit is required")
	}
	if m.OS == "" || m.Arch == "" || m.HardwareLabel == "" {
		return fmt.Errorf("locked manifest requires os, arch, and hardware_label")
	}
	if len(m.ToolchainVersions) == 0 {
		return fmt.Errorf("locked manifest requires toolchain versions")
	}
	for _, v := range m.ToolchainVersions {
		if v.Name == "" || v.Version == "" {
			return fmt.Errorf("toolchain entry needs name and version: %+v", v)
		}
	}
	if m.Sidecar.Commit == "" || !validHash(m.Sidecar.BinarySHA256) || m.Sidecar.Version == "" {
		return fmt.Errorf("locked manifest requires complete sidecar identity")
	}
	if len(m.Sidecar.Capabilities) == 0 {
		return fmt.Errorf("sidecar capability set is required")
	}
	if m.ProviderProfile == "" {
		return fmt.Errorf("provider_profile is required")
	}
	if len(m.StageRoutes) == 0 {
		return fmt.Errorf("locked manifest requires resolved stage routes")
	}
	if len(m.TaskHashes) != len(m.Tasks) {
		return fmt.Errorf("task_hashes contains %d entries for %d tasks", len(m.TaskHashes), len(m.Tasks))
	}
	hashed := make(map[string]bool, len(m.TaskHashes))
	for i, h := range m.TaskHashes {
		if err := h.Validate(); err != nil {
			return fmt.Errorf("task_hashes[%d]: %w", i, err)
		}
		if hashed[h.Name] {
			return fmt.Errorf("duplicate task hash entry %q", h.Name)
		}
		hashed[h.Name] = true
	}
	for _, task := range m.Tasks {
		if !hashed[task.ID] {
			return fmt.Errorf("task_hashes has no entry for task %s", task.ID)
		}
	}
	if len(m.RuleHashes) != 4 {
		return fmt.Errorf("locked manifest requires exactly four rule hashes: invalidation, retry, stopping, and security-review")
	}
	seenRules := make(map[string]bool, len(m.RuleHashes))
	for i, h := range m.RuleHashes {
		if err := h.Validate(); err != nil {
			return fmt.Errorf("rule_hashes[%d]: %w", i, err)
		}
		if seenRules[h.Name] {
			return fmt.Errorf("duplicate rule hash entry %q", h.Name)
		}
		seenRules[h.Name] = true
	}
	for _, name := range []string{"invalidation", "retry", "stopping", "security-review"} {
		if !seenRules[name] {
			return fmt.Errorf("locked manifest is missing %s rule hash", name)
		}
	}
	taskIDs := make([]string, 0, len(m.Tasks))
	for _, task := range m.Tasks {
		if !task.Sealed {
			return fmt.Errorf("task %s is not sealed; a locked manifest requires sealed tasks only", task.ID)
		}
		taskIDs = append(taskIDs, task.ID)
	}
	// DE1: locked task hashes must prove contents, not just syntax. Recompute
	// each task's canonical hash from its spec and compare against the
	// manifest entry; a stale or forged hash names the offending task.
	recomputed := make(map[string]string, len(m.Tasks))
	for _, task := range m.Tasks {
		hash, err := CanonicalTaskHash(task)
		if err != nil {
			return fmt.Errorf("recompute task hash for %s: %w", task.ID, err)
		}
		recomputed[task.ID] = hash
	}
	for i, h := range m.TaskHashes {
		if want := recomputed[h.Name]; want != "" && h.SHA256 != want {
			return fmt.Errorf("task_hashes[%d] for task %s does not match its content: recomputed %s", i, h.Name, want)
		}
	}
	if err := m.Schedule.CompleteFor(m.Protocol, taskIDs); err != nil {
		return err
	}
	wantSample := len(taskIDs) * len(m.Protocol.Arms) * m.Protocol.Repetitions
	if len(m.Schedule.Trials) != wantSample {
		return fmt.Errorf("sample size = %d scheduled trials, want tasks x arms x repetitions = %d",
			len(m.Schedule.Trials), wantSample)
	}
	if m.SampleSize != len(m.Schedule.Trials) {
		return fmt.Errorf("declared sample_size %d does not match %d scheduled trials", m.SampleSize, len(m.Schedule.Trials))
	}
	if m.ExpectedCalls <= 0 {
		return fmt.Errorf("expected_calls must be positive, got %d", m.ExpectedCalls)
	}
	if !finite(m.EstimatedCostUSD) || m.EstimatedCostUSD <= 0 {
		return fmt.Errorf("estimated_cost_usd must be finite and positive, got %v", m.EstimatedCostUSD)
	}
	if m.EstimatedCostUSD > m.Protocol.HardSpendCapUSD {
		return fmt.Errorf("estimated cost %.2f exceeds the hard spend cap %.2f",
			m.EstimatedCostUSD, m.Protocol.HardSpendCapUSD)
	}
	return nil
}
