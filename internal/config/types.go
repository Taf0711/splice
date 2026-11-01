package config

import (
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/flags"
)

const OpenAIBaseURL = "https://api.openai.com/v1"
const AnthropicBaseURL = "https://api.anthropic.com"
const GoogleBaseURL = "https://generativelanguage.googleapis.com"

type ProviderKind string

const (
	ProviderKindOpenAI           ProviderKind = "openai"
	ProviderKindAnthropic        ProviderKind = "anthropic"
	ProviderKindAnthropicCompat  ProviderKind = "anthropic-compatible"
	ProviderKindGoogle           ProviderKind = "google"
	ProviderKindOpenAICompatible ProviderKind = "openai-compatible"
)

type ProviderProfile struct {
	Name         string       `json:"name"`
	Provider     string       `json:"provider,omitempty"`
	ProviderKind ProviderKind `json:"provider_kind,omitempty"`
	CatalogID    string       `json:"catalogID,omitempty"`
	BaseURL      string       `json:"baseURL,omitempty"`
	APIKey       string       `json:"apiKey,omitempty"`
	APIKeyEnv    string       `json:"apiKeyEnv,omitempty"`
	// APIKeyStored marks that this provider's API key lives in the encrypted
	// credential store (internal/credstore), not inline in APIKey. The effective
	// key is loaded from the store at provider-build time; config.json holds only
	// this marker, never the secret.
	APIKeyStored    bool              `json:"apiKeyStored,omitempty"`
	APIFormat       string            `json:"apiFormat,omitempty"`
	AuthHeader      string            `json:"authHeader,omitempty"`
	AuthScheme      string            `json:"authScheme,omitempty"`
	AuthHeaderValue string            `json:"authHeaderValue,omitempty"`
	CustomHeaders   map[string]string `json:"customHeaders,omitempty"`
	Model           string            `json:"model,omitempty"`
	ParseThinkTags  *bool             `json:"parseThinkTags,omitempty"`
	Description     string            `json:"description,omitempty"`
	// ReasoningEffort is an optional per-profile reasoning effort preference
	// (low/medium/high/...), seeded into the TUI session and exec runs.
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

func HasProviderProfile(profile ProviderProfile) bool {
	return strings.TrimSpace(profile.Name) != "" ||
		strings.TrimSpace(profile.Provider) != "" ||
		strings.TrimSpace(string(profile.ProviderKind)) != "" ||
		strings.TrimSpace(profile.CatalogID) != "" ||
		strings.TrimSpace(profile.BaseURL) != "" ||
		strings.TrimSpace(profile.APIKey) != "" ||
		strings.TrimSpace(profile.APIKeyEnv) != "" ||
		strings.TrimSpace(profile.APIFormat) != "" ||
		strings.TrimSpace(profile.AuthHeader) != "" ||
		strings.TrimSpace(profile.AuthScheme) != "" ||
		strings.TrimSpace(profile.AuthHeaderValue) != "" ||
		profile.CustomHeaders != nil ||
		strings.TrimSpace(profile.Model) != "" ||
		profile.ParseThinkTags != nil ||
		strings.TrimSpace(profile.Description) != "" ||
		strings.TrimSpace(profile.ReasoningEffort) != ""
}

type SandboxConfig struct {
	// Network controls whether shell commands classified as network-touching
	// (curl, git push, package installs, …) are allowed: "allow" or "deny".
	// Empty keeps the built-in default (deny). Without this knob the engine's
	// hard-coded NetworkDeny was unreachable from any config surface.
	Network string `json:"network,omitempty"`
	// AdditionalWriteRoots lists directories outside the workspace the sandbox
	// allows writes in. Each entry must be an existing directory; entries are
	// normalized (~-expanded, absolutized, symlink-resolved) at startup and an
	// invalid entry fails the run. Honored from the GLOBAL user config and CLI
	// flags only — deliberately not project config, so a cloned repo cannot
	// grant itself write access. Session-only grants use /add-dir instead.
	AdditionalWriteRoots []string `json:"additionalWriteRoots,omitempty"`
	// AdditionalReadRoots lists directories outside the workspace the sandbox
	// allows reads in, without granting write access. Entries are normalized
	// the same way as AdditionalWriteRoots. Honored from the GLOBAL user config
	// and CLI flags only — deliberately not project config, so a cloned repo
	// cannot grant itself read access to the user's home directory.
	AdditionalReadRoots []string `json:"additionalReadRoots,omitempty"`
	// AllowRead lists subtrees that the sandbox may re-include inside its
	// default credential deny roots. Honored from global user config and CLI
	// overrides only. Project config cannot use this field to read user secrets.
	AllowRead []string `json:"allowRead,omitempty"`
	// BlockUnixSockets, when true, asks the Linux sandbox helper to install a
	// best-effort seccomp filter that denies AF_UNIX socket creation inside the
	// sandboxed command. Off by default; ignored on non-Linux backends.
	BlockUnixSockets bool `json:"blockUnixSockets,omitempty"`
	// MonitorDenials, when true on macOS, tails the unified log for this run's
	// sandbox denials and appends them to a command's stderr so blocked operations
	// are visible to the agent. Off by default. No-op on platforms/OS versions that
	// do not deliver seatbelt denials to the queryable log.
	MonitorDenials bool `json:"monitorDenials,omitempty"`
}

type NotifyConfig struct {
	Mode      string `json:"mode,omitempty"`
	FocusMode string `json:"focusMode,omitempty"`
}

type ToolsConfig struct {
	DeferThreshold    int `json:"deferThreshold,omitempty"`
	deferThresholdSet bool
}

type PreferencesConfig struct {
	FavoriteModels []string `json:"favoriteModels,omitempty"`
	// Theme is the persisted TUI palette preference — "auto" or a registered theme
	// name (e.g. "dracula"). Applied at startup below the --theme flag and
	// SPLICE_THEME, so a /theme choice survives restart. Empty = unset (defaults auto).
	Theme string `json:"theme,omitempty"`
	// Recaps is a tri-state: nil (unset) defaults to ON; an explicit false means
	// the user turned post-turn recaps off. A *bool is its own tri-state, so no
	// custom unmarshal is needed (unlike ToolsConfig.DeferThreshold's int).
	Recaps *bool `json:"recaps,omitempty"`
	// Layout is the persisted TUI layout preset name (default/compact/
	// execution/review/minimal). Applied at startup; empty = unset (the
	// built-in defaults).
	Layout string `json:"layout,omitempty"`
}

// RecapsEnabled reports whether post-turn recaps are on. Unset defaults to ON.
func (p PreferencesConfig) RecapsEnabled() bool {
	return p.Recaps == nil || *p.Recaps
}

// CompactionConfig configures agent-loop context compaction. It mirrors pi's
// compaction settings so a user can tune Splice the same way. All fields are
// optional; zero/nil values keep the built-in default behavior.
type CompactionConfig struct {
	// Enabled gates compaction. nil (unset) defaults to ON, matching the
	// pre-existing behavior. An explicit false disables compaction (the TUI
	// then sets the agent ContextWindow to 0). Tri-state like Recaps.
	Enabled *bool `json:"enabled,omitempty"`
	// StayInCheapestPricingTier caps registry-known model windows at the lowest
	// positive pricing boundary. nil (unset) defaults to ON.
	StayInCheapestPricingTier *bool `json:"stayInCheapestPricingTier,omitempty"`
	// ReserveTokens is the response budget reserved below the context window:
	// compaction fires when estimated tokens exceed ContextWindow - ReserveTokens
	// (pi-style), instead of the built-in 0.7 ratio. 0 keeps the ratio default.
	ReserveTokens int `json:"reserveTokens,omitempty"`
	// KeepRecentTokens is the trailing-token budget kept verbatim (pi-style),
	// instead of the built-in 6-message preserve count. 0 keeps the
	// message-count default.
	KeepRecentTokens int `json:"keepRecentTokens,omitempty"`
}

// EnabledOrDefault reports whether compaction is on. Unset defaults to ON.
func (c CompactionConfig) EnabledOrDefault() bool {
	return c.Enabled == nil || *c.Enabled
}

// StayInCheapestPricingTierOrDefault reports whether the compaction budget is
// capped at the cheapest pricing tier. Unset defaults to ON.
func (c CompactionConfig) StayInCheapestPricingTierOrDefault() bool {
	return c.StayInCheapestPricingTier == nil || *c.StayInCheapestPricingTier
}

// KeyBindingDef defines one key binding string (e.g. "ctrl+o") that the TUI
// can remap. An empty string means "use the built-in default" for that action.
type KeyBindingDef string

// KeyBindingsConfig holds the subset of TUI keybindings that users may remap
// via config.json. Each field defaults to a sensible built-in when empty.
type KeyBindingsConfig struct {
	// ToggleDetailed toggles the detailed transcript view (default: ctrl+o).
	ToggleDetailed KeyBindingDef `json:"toggleDetailed,omitempty"`
	// ToggleMouse toggles mouse capture release (default: ctrl+e).
	ToggleMouse KeyBindingDef `json:"toggleMouse,omitempty"`
	// CycleReasoning cycles through reasoning effort levels (default: ctrl+t).
	CycleReasoning KeyBindingDef `json:"cycleReasoning,omitempty"`
	// TogglePlan toggles the plan panel expansion (default: ctrl+p).
	TogglePlan KeyBindingDef `json:"togglePlan,omitempty"`
	// ToggleSidebar toggles the right context sidebar (default: ctrl+b).
	ToggleSidebar KeyBindingDef `json:"toggleSidebar,omitempty"`
	// ToggleNarration cycles the transcript verbosity quiet/normal/detailed
	// (default: ctrl+n).
	ToggleNarration KeyBindingDef `json:"toggleNarration,omitempty"`
}

// LocalControlConfig controls local browser/desktop/terminal automation helpers.
// Helpers are discovered lazily by the tool that needs them; no setup command or
// background probe is required during startup.
type LocalControlConfig struct {
	Enabled      bool                     `json:"enabled,omitempty"`
	Browser      LocalControlDriverConfig `json:"browser,omitempty"`
	Desktop      LocalControlDriverConfig `json:"desktop,omitempty"`
	Terminal     LocalControlDriverConfig `json:"terminal,omitempty"`
	ArtifactsDir string                   `json:"artifactsDir,omitempty"`
	enabledSet   bool
}

type LocalControlDriverConfig struct {
	Enabled    bool   `json:"enabled,omitempty"`
	HelperPath string `json:"helperPath,omitempty"`
	Driver     string `json:"driver,omitempty"`
	enabledSet bool
}

func (cfg LocalControlConfig) BrowserEnabled() bool {
	return !cfg.Disabled() && (!cfg.Browser.enabledSet || cfg.Browser.Enabled)
}

func (cfg LocalControlConfig) DesktopEnabled() bool {
	return !cfg.Disabled() && cfg.Desktop.enabledSet && cfg.Desktop.Enabled
}

func (cfg LocalControlConfig) TerminalEnabled() bool {
	return !cfg.Disabled() && (!cfg.Terminal.enabledSet || cfg.Terminal.Enabled)
}

func (cfg LocalControlConfig) Disabled() bool {
	return cfg.enabledSet && !cfg.Enabled
}

func (cfg LocalControlConfig) Empty() bool {
	return !cfg.Enabled &&
		!cfg.enabledSet &&
		cfg.ArtifactsDir == "" &&
		cfg.Browser.Empty() &&
		cfg.Desktop.Empty() &&
		cfg.Terminal.Empty()
}

func (cfg LocalControlDriverConfig) Empty() bool {
	return !cfg.Enabled &&
		!cfg.enabledSet &&
		cfg.HelperPath == "" &&
		cfg.Driver == ""
}

// SwarmConfig tunes the multi-agent swarm. MaxTeamSize caps how many members run
// concurrently per team; 0 uses the built-in default (8). Spawns past the cap
// queue and launch as slots free, so lowering it bounds parallelism (and provider
// load / rate-limit pressure) without dropping work.
type SwarmConfig struct {
	MaxTeamSize int `json:"maxTeamSize,omitempty"`
}

// WorktreesConfig controls isolated TUI pipeline worktrees.
type WorktreesConfig struct {
	// Enabled gates TUI pipeline isolation. nil defaults to on.
	Enabled *bool `json:"enabled,omitempty"`
	// Directory is the base directory for created worktrees.
	Directory string `json:"directory,omitempty"`
}

// VerifyConfig controls the deterministic test_runner command. An empty
// command keeps the built-in test detection behavior.
type VerifyConfig struct {
	TestCommand []string `json:"testCommand,omitempty"`
	TestDir     string   `json:"testDir,omitempty"`
}

// Validate checks that the test directory is a relative path inside the
// workspace. The workspace root is applied by the pipeline at run time.
func (c VerifyConfig) Validate() error {
	dir := strings.TrimSpace(c.TestDir)
	if dir == "" {
		return nil
	}
	if filepath.IsAbs(dir) || strings.HasPrefix(dir, `/`) || strings.HasPrefix(dir, `\\`) {
		return fmt.Errorf("invalid verify.testDir %q: must be relative to the workspace", c.TestDir)
	}
	for _, part := range strings.FieldsFunc(dir, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return fmt.Errorf("invalid verify.testDir %q: must stay inside the workspace", c.TestDir)
		}
	}
	return nil
}

// PipelineConfig holds the user limits for deterministic pipeline runs.
type PipelineConfig struct {
	// MaxWallSeconds is the wall-time limit of one pipeline run, in seconds.
	// Zero uses the built-in default of 600 seconds.
	MaxWallSeconds int `json:"max_wall_seconds,omitempty"`
}

// EnabledOrDefault reports whether TUI pipeline worktrees are on.
func (c WorktreesConfig) EnabledOrDefault() bool {
	return c.Enabled == nil || *c.Enabled
}

// ToolsOverride builds a ToolsConfig that explicitly overrides the deferred-tool
// threshold (including to 0, which disables deferral). Use this for programmatic
// Overrides — a bare ToolsConfig{DeferThreshold: 0} is indistinguishable from
// "unset" and will not override.
func ToolsOverride(deferThreshold int) ToolsConfig {
	return ToolsConfig{DeferThreshold: deferThreshold, deferThresholdSet: true}
}

func (cfg *ToolsConfig) UnmarshalJSON(data []byte) error {
	type rawTools struct {
		DeferThreshold *int `json:"deferThreshold"`
	}
	var raw rawTools
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	cfg.DeferThreshold = 0
	cfg.deferThresholdSet = false
	if raw.DeferThreshold != nil {
		cfg.DeferThreshold = *raw.DeferThreshold
		cfg.deferThresholdSet = true
	}
	return nil
}

type AuthConfig struct {
	Storage string `json:"storage,omitempty"`
}

// DecisionAdvisorLimits are the advisor's spending and footprint caps. The
// JSON keys match the spec's profile. Omitted fields take the spec's
// proposed starting values, which are not spending authority: the global
// task budget contains the advisor allowance and never extends it.
type DecisionAdvisorLimits struct {
	ReservationUSDPerMillionInput float64 `json:"reservation_usd_per_million_input,omitempty"`
	MaxRequestsPerEpisode         int     `json:"max_requests_per_episode,omitempty"`
	MaxCandidates                 int     `json:"max_candidates,omitempty"`
	MaxDependencyGroups           int     `json:"max_dependency_groups,omitempty"`
	MaxSourceFiles                int     `json:"max_source_files,omitempty"`
	MaxRecentEvents               int     `json:"max_recent_events,omitempty"`
	MaxRequestBytes               int     `json:"max_request_bytes,omitempty"`
	MaxResponseBytes              int     `json:"max_response_bytes,omitempty"`
	TimeoutMS                     int     `json:"timeout_ms,omitempty"`
	MaxTotalOptionalWaitMS        int     `json:"max_total_optional_wait_ms,omitempty"`
	MaxInterventionsPerEpisode    int     `json:"max_interventions_per_episode,omitempty"`
	Retries                       int     `json:"retries,omitempty"`
	MaxAdvisorCostUSD             float64 `json:"max_advisor_cost_usd,omitempty"`
	MaxFractionOfBaselineCost     float64 `json:"max_fraction_of_baseline_cost,omitempty"`
}

// DecisionAdvisorConfig is the decision_advisor configuration section. The
// zero value is mode "off": absent configuration never calls the provider.
type DecisionAdvisorConfig struct {
	Backend         string `json:"backend,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"`
	APIKeyEnv       string `json:"api_key_env,omitempty"`
	ProviderProfile string `json:"provider_profile,omitempty"`
	// Mode is off, shadow, active_experimental, or active.
	Mode string `json:"mode,omitempty"`
	// Profile is the pinned decision model ID (for example
	// "typesafe/jev-1.13"). Required when mode is not off.
	Profile string `json:"profile,omitempty"`
	// QuestionPack pins the versioned question pack.
	QuestionPack string `json:"question_pack,omitempty"`
	// Policy pins the versioned host policy.
	Policy string                `json:"policy,omitempty"`
	Limits DecisionAdvisorLimits `json:"limits,omitempty"`
	// ReasoningEffort is sent with each decision request when set (low, medium, high).
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// TrajectoryDefault marks the section the resolver built for the default
	// trajectory classifier. It is set in code, never read from config, and
	// it keeps the selector off.
	TrajectoryDefault bool `json:"-"`
	// ExplicitInactive is set in code when the default classifier replaced a
	// configured section that is dormant (pipeline.decision_advisor is off).
	// The section's own settings are not used. Doctor reports the note.
	ExplicitInactive bool `json:"-"`
}

// DefaultDecisionAdvisorLimits are the spec's proposed starting limits.
func DefaultDecisionAdvisorLimits() DecisionAdvisorLimits {
	return DecisionAdvisorLimits{
		ReservationUSDPerMillionInput: 0.042,
		MaxRequestsPerEpisode:         4,
		MaxCandidates:                 4,
		MaxDependencyGroups:           1,
		MaxSourceFiles:                2,
		MaxRecentEvents:               4,
		MaxRequestBytes:               16384,
		MaxResponseBytes:              65536,
		TimeoutMS:                     1500,
		MaxTotalOptionalWaitMS:        3000,
		MaxInterventionsPerEpisode:    1,
		Retries:                       0,
		MaxAdvisorCostUSD:             0.001,
		MaxFractionOfBaselineCost:     0.10,
	}
}

// WithDefaults fills omitted limits without a network call.
func (c DecisionAdvisorConfig) WithDefaults() DecisionAdvisorConfig {
	if c.Backend == "" {
		c.Backend = "openrouter_decisions"
	}
	defaults := DefaultDecisionAdvisorLimits()
	if c.Limits.MaxRequestsPerEpisode == 0 {
		c.Limits.MaxRequestsPerEpisode = defaults.MaxRequestsPerEpisode
	}
	if c.Limits.MaxCandidates == 0 {
		c.Limits.MaxCandidates = defaults.MaxCandidates
	}
	if c.Limits.MaxDependencyGroups == 0 {
		c.Limits.MaxDependencyGroups = defaults.MaxDependencyGroups
	}
	if c.Limits.MaxSourceFiles == 0 {
		c.Limits.MaxSourceFiles = defaults.MaxSourceFiles
	}
	if c.Limits.MaxRecentEvents == 0 {
		c.Limits.MaxRecentEvents = defaults.MaxRecentEvents
	}
	if c.Limits.MaxRequestBytes == 0 {
		c.Limits.MaxRequestBytes = defaults.MaxRequestBytes
	}
	if c.Limits.MaxResponseBytes == 0 {
		c.Limits.MaxResponseBytes = defaults.MaxResponseBytes
	}
	if c.Limits.TimeoutMS == 0 {
		c.Limits.TimeoutMS = defaults.TimeoutMS
	}
	if c.Limits.MaxTotalOptionalWaitMS == 0 {
		c.Limits.MaxTotalOptionalWaitMS = defaults.MaxTotalOptionalWaitMS
	}
	if c.Limits.MaxInterventionsPerEpisode == 0 {
		c.Limits.MaxInterventionsPerEpisode = defaults.MaxInterventionsPerEpisode
	}
	if c.Limits.MaxAdvisorCostUSD == 0 {
		c.Limits.MaxAdvisorCostUSD = defaults.MaxAdvisorCostUSD
	}
	if c.Limits.MaxFractionOfBaselineCost == 0 {
		c.Limits.MaxFractionOfBaselineCost = defaults.MaxFractionOfBaselineCost
	}
	if c.Limits.ReservationUSDPerMillionInput == 0 {
		c.Limits.ReservationUSDPerMillionInput = defaults.ReservationUSDPerMillionInput
	}
	return c
}

// Validate checks the advisor section with the offending field named.
// Mode "off" accepts the rest of the section untouched: nothing calls, so
// nothing else can be wrong at runtime.
func (c DecisionAdvisorConfig) Validate() error {
	switch c.Mode {
	case "", "off", "shadow", "active_experimental", "active":
	default:
		return fmt.Errorf("invalid decision_advisor.mode %q: expected off, shadow, active_experimental, or active", c.Mode)
	}
	switch c.Backend {
	case "", "openrouter_decisions":
	case "http":
		if strings.TrimSpace(c.Endpoint) == "" {
			return fmt.Errorf("decision_advisor.endpoint: required for http backend")
		}
	case "provider":
		if strings.TrimSpace(c.ProviderProfile) == "" {
			return fmt.Errorf("decision_advisor.provider_profile: required for provider backend")
		}
	default:
		return fmt.Errorf("decision_advisor.backend: unknown backend %q", c.Backend)
	}
	if c.Limits.ReservationUSDPerMillionInput < 0 || math.IsNaN(c.Limits.ReservationUSDPerMillionInput) || math.IsInf(c.Limits.ReservationUSDPerMillionInput, 0) {
		return fmt.Errorf("decision_advisor.limits.reservation_usd_per_million_input: invalid price")
	}
	if c.Mode == "" || c.Mode == "off" {
		return nil
	}
	if strings.TrimSpace(c.Profile) == "" && c.Backend != "provider" {
		return fmt.Errorf("invalid decision_advisor.profile: required when mode is %q", c.Mode)
	}
	if c.Limits.Retries != 0 {
		return fmt.Errorf("invalid decision_advisor.limits.retries %d: must be 0, hidden retries are disabled for the advisor", c.Limits.Retries)
	}
	if c.Limits.MaxRequestBytes <= 0 {
		return fmt.Errorf("invalid decision_advisor.limits.max_request_bytes %d: must be positive", c.Limits.MaxRequestBytes)
	}
	if c.Limits.MaxResponseBytes <= 0 {
		return fmt.Errorf("invalid decision_advisor.limits.max_response_bytes %d: must be positive", c.Limits.MaxResponseBytes)
	}
	if c.Limits.TimeoutMS <= 0 {
		return fmt.Errorf("invalid decision_advisor.limits.timeout_ms %d: must be positive", c.Limits.TimeoutMS)
	}
	if c.Limits.MaxAdvisorCostUSD < 0 {
		return fmt.Errorf("invalid decision_advisor.limits.max_advisor_cost_usd %v: must not be negative", c.Limits.MaxAdvisorCostUSD)
	}
	if c.Limits.MaxFractionOfBaselineCost < 0 || c.Limits.MaxFractionOfBaselineCost > 1 {
		return fmt.Errorf("invalid decision_advisor.limits.max_fraction_of_baseline_cost %v: must be within [0,1]", c.Limits.MaxFractionOfBaselineCost)
	}
	return nil
}

// TrajectoryConfig sets the probability thresholds for host actions.
type TrajectoryConfig struct {
	Thresholds map[string]float64 `json:"thresholds,omitempty"`
}

// Validate checks the action labels and probability thresholds.
func (c TrajectoryConfig) Validate() error {
	for label, p := range c.Thresholds {
		switch label {
		case "continue", "step_back", "rollback", "escalate", "stop":
		default:
			return fmt.Errorf("trajectory.thresholds: unknown label %q", label)
		}
		if math.IsNaN(p) || math.IsInf(p, 0) || p < 0 || p > 1 {
			return fmt.Errorf("trajectory.thresholds.%s: probability %v outside [0,1]", label, p)
		}
	}
	return nil
}

type FileConfig struct {
	ActiveProvider      string                `json:"activeProvider,omitempty"`
	Providers           []ProviderProfile     `json:"providers,omitempty"`
	Auth                AuthConfig            `json:"auth,omitempty"`
	MaxTurns            int                   `json:"maxTurns,omitempty"`
	MCP                 MCPConfig             `json:"mcp,omitempty"`
	Sandbox             SandboxConfig         `json:"sandbox,omitempty"`
	Notify              NotifyConfig          `json:"notify,omitempty"`
	Tools               ToolsConfig           `json:"tools,omitempty"`
	Swarm               SwarmConfig           `json:"swarm,omitempty"`
	Preferences         PreferencesConfig     `json:"preferences,omitempty"`
	KeyBindings         KeyBindingsConfig     `json:"keybindings,omitempty"`
	LocalControl        LocalControlConfig    `json:"localControl,omitempty"`
	Compaction          CompactionConfig      `json:"compaction,omitempty"`
	Worktrees           WorktreesConfig       `json:"worktrees,omitempty"`
	Verify              VerifyConfig          `json:"verify,omitempty"`
	DecisionAdvisor     DecisionAdvisorConfig `json:"decisionAdvisor,omitempty"`
	Trajectory          TrajectoryConfig      `json:"trajectory,omitempty"`
	Pipeline            PipelineConfig        `json:"pipeline,omitempty"`
	DefaultProjectTrust string                `json:"defaultProjectTrust,omitempty"`
	// Flags holds per-feature switches as declared in internal/flags. Only
	// registered names resolve; an unknown name fails loudly. Project config
	// may set ScopeProject flags only.
	Flags map[string]bool `json:"flags,omitempty"`
}

func (cfg FileConfig) MarshalJSON() ([]byte, error) {
	type rawConfig struct {
		ActiveProvider      string                 `json:"activeProvider,omitempty"`
		Providers           []ProviderProfile      `json:"providers,omitempty"`
		Auth                *AuthConfig            `json:"auth,omitempty"`
		MaxTurns            int                    `json:"maxTurns,omitempty"`
		MCP                 MCPConfig              `json:"mcp,omitempty"`
		Sandbox             SandboxConfig          `json:"sandbox,omitempty"`
		Notify              NotifyConfig           `json:"notify,omitempty"`
		Tools               ToolsConfig            `json:"tools,omitempty"`
		Swarm               SwarmConfig            `json:"swarm,omitempty"`
		Preferences         PreferencesConfig      `json:"preferences,omitempty"`
		KeyBindings         KeyBindingsConfig      `json:"keybindings,omitempty"`
		LocalControl        *LocalControlConfig    `json:"localControl,omitempty"`
		Compaction          CompactionConfig       `json:"compaction,omitempty"`
		Worktrees           WorktreesConfig        `json:"worktrees,omitempty"`
		Verify              *VerifyConfig          `json:"verify,omitempty"`
		DecisionAdvisor     *DecisionAdvisorConfig `json:"decisionAdvisor,omitempty"`
		Trajectory          *TrajectoryConfig      `json:"trajectory,omitempty"`
		Pipeline            *PipelineConfig        `json:"pipeline,omitempty"`
		DefaultProjectTrust string                 `json:"defaultProjectTrust,omitempty"`
		Flags               map[string]bool        `json:"flags,omitempty"`
	}
	raw := rawConfig{
		ActiveProvider:      cfg.ActiveProvider,
		Providers:           cfg.Providers,
		MaxTurns:            cfg.MaxTurns,
		MCP:                 cfg.MCP,
		Sandbox:             cfg.Sandbox,
		Notify:              cfg.Notify,
		Tools:               cfg.Tools,
		Swarm:               cfg.Swarm,
		Preferences:         cfg.Preferences,
		KeyBindings:         cfg.KeyBindings,
		Compaction:          cfg.Compaction,
		Worktrees:           cfg.Worktrees,
		DefaultProjectTrust: cfg.DefaultProjectTrust,
		Flags:               cfg.Flags,
	}
	if len(cfg.Verify.TestCommand) > 0 || strings.TrimSpace(cfg.Verify.TestDir) != "" {
		verify := cfg.Verify
		raw.Verify = &verify
	}
	if cfg.DecisionAdvisor != (DecisionAdvisorConfig{}) {
		advisor := cfg.DecisionAdvisor
		raw.DecisionAdvisor = &advisor
	}
	if len(cfg.Trajectory.Thresholds) > 0 {
		trajectory := cfg.Trajectory
		raw.Trajectory = &trajectory
	}
	if cfg.Pipeline != (PipelineConfig{}) {
		pipeline := cfg.Pipeline
		raw.Pipeline = &pipeline
	}
	if strings.TrimSpace(cfg.Auth.Storage) != "" {
		raw.Auth = &cfg.Auth
	}
	if !cfg.LocalControl.Empty() {
		raw.LocalControl = &cfg.LocalControl
	}
	return json.Marshal(raw)
}

type ResolveOptions struct {
	UserConfigPath    string
	ProjectConfigPath string
	// ProjectTrusted gates the project layer. ProjectConfigPath is read and
	// merged only when this is true. The zero value keeps the project layer out,
	// so an untrusted workspace cannot change behavior through .splice/config.json.
	ProjectTrusted  bool
	ProviderCommand string
	Env             map[string]string
	Overrides       Overrides
}

type Overrides struct {
	ActiveProvider string
	Providers      []ProviderProfile
	Provider       ProviderProfile
	MaxTurns       int
	MCP            MCPConfig
	Sandbox        SandboxConfig
	Notify         NotifyConfig
	Tools          ToolsConfig
	KeyBindings    KeyBindingsConfig
	LocalControl   LocalControlConfig
	Worktrees      WorktreesConfig
	Flags          map[string]bool
	// ProjectTrust carries a launch's workspace trust decision to the CLI
	// config resolver, which sets ResolveOptions.ProjectTrusted from it. Nil
	// means the resolver decides trust itself. Resolve does not read it.
	ProjectTrust *bool
}

type ResolvedConfig struct {
	ActiveProvider      string
	Providers           []ProviderProfile
	Provider            ProviderProfile
	Auth                AuthConfig
	MaxTurns            int
	Compaction          CompactionConfig
	MCP                 MCPConfig
	Sandbox             SandboxConfig
	Notify              NotifyConfig
	Tools               ToolsConfig
	Swarm               SwarmConfig
	Preferences         PreferencesConfig
	KeyBindings         KeyBindingsConfig
	LocalControl        LocalControlConfig
	Worktrees           WorktreesConfig
	Verify              VerifyConfig
	DecisionAdvisor     DecisionAdvisorConfig
	Trajectory          TrajectoryConfig
	DefaultProjectTrust string
	// Flags is the resolved flag set. The zero value means all defaults.
	Flags flags.Set
	// FlagResolutions explains each registry flag: its value and the source
	// that set it. It comes from the same resolution as Flags.
	FlagResolutions []flags.Resolution
	// MaxWall is the wall-time limit of one pipeline run from the user
	// config or SPLICE_MAX_WALL. Zero means the pipeline default.
	MaxWall time.Duration
}

type MCPConfig struct {
	Servers map[string]MCPServerConfig `json:"servers,omitempty"`
}

type MCPServerConfig struct {
	Type        string            `json:"type,omitempty"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	URL         string            `json:"url,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	Auth        string            `json:"auth,omitempty"`
	OAuth       *MCPOAuthConfig   `json:"oauth,omitempty"`
	Disabled    bool              `json:"disabled,omitempty"`
	disabledSet bool
}

// MCPOAuthConfig describes how to authenticate to a remote MCP server using an
// OAuth 2.0 + PKCE authorization-code flow. Endpoints may be discovered from the
// authorization server's metadata document; explicit values here override or
// fill in anything discovery cannot provide. Client credentials are optional
// when the server supports dynamic client registration.
type MCPOAuthConfig struct {
	ClientID              string   `json:"clientID,omitempty"`
	ClientSecret          string   `json:"clientSecret,omitempty"`
	Scopes                []string `json:"scopes,omitempty"`
	AuthorizationEndpoint string   `json:"authorizationEndpoint,omitempty"`
	TokenEndpoint         string   `json:"tokenEndpoint,omitempty"`
	RegistrationEndpoint  string   `json:"registrationEndpoint,omitempty"`
	IssuerURL             string   `json:"issuerURL,omitempty"`
}

func (cfg *FileConfig) UnmarshalJSON(data []byte) error {
	type rawConfig struct {
		DecisionAdvisor      DecisionAdvisorConfig      `json:"decisionAdvisor"`
		DecisionAdvisorSnake *DecisionAdvisorConfig     `json:"decision_advisor"`
		Trajectory           TrajectoryConfig           `json:"trajectory"`
		Pipeline             PipelineConfig             `json:"pipeline"`
		ActiveProvider       string                     `json:"activeProvider"`
		Providers            []ProviderProfile          `json:"providers"`
		Auth                 AuthConfig                 `json:"auth"`
		MaxTurns             int                        `json:"maxTurns"`
		MCP                  MCPConfig                  `json:"mcp"`
		Sandbox              SandboxConfig              `json:"sandbox"`
		Notify               NotifyConfig               `json:"notify"`
		Tools                ToolsConfig                `json:"tools"`
		Swarm                SwarmConfig                `json:"swarm"`
		Preferences          PreferencesConfig          `json:"preferences"`
		KeyBindings          KeyBindingsConfig          `json:"keybindings"`
		LocalControl         LocalControlConfig         `json:"localControl"`
		Compaction           CompactionConfig           `json:"compaction"`
		Worktrees            WorktreesConfig            `json:"worktrees"`
		Verify               VerifyConfig               `json:"verify"`
		DefaultProjectTrust  string                     `json:"defaultProjectTrust"`
		Flags                map[string]bool            `json:"flags"`
		MCPServers           map[string]MCPServerConfig `json:"mcpServers"`
		MCPServersSnake      map[string]MCPServerConfig `json:"mcp_servers"`
	}

	var raw rawConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	cfg.ActiveProvider = raw.ActiveProvider
	cfg.Providers = raw.Providers
	cfg.Auth.Storage = strings.ToLower(strings.TrimSpace(raw.Auth.Storage))
	// A negative maxTurns is unambiguously invalid; without this it would be
	// silently dropped by the `MaxTurns > 0` merge gates and fall back to the
	// default, hiding a misconfiguration. (0 is left as-is: with omitempty it is
	// indistinguishable from "unset" and means "use the default".) The CLI flag
	// rejects 0 too because there an explicit "0" is distinguishable from absent.
	if raw.MaxTurns < 0 {
		return fmt.Errorf("invalid maxTurns %d: must be >= 0", raw.MaxTurns)
	}
	cfg.MaxTurns = raw.MaxTurns
	cfg.MCP = raw.MCP
	cfg.Sandbox = raw.Sandbox
	cfg.Notify = raw.Notify
	cfg.Tools = raw.Tools
	cfg.Swarm = raw.Swarm
	cfg.Preferences = raw.Preferences
	cfg.KeyBindings = raw.KeyBindings
	cfg.LocalControl = raw.LocalControl
	cfg.Compaction = raw.Compaction
	cfg.Worktrees = raw.Worktrees
	cfg.Verify = raw.Verify
	cfg.DecisionAdvisor = raw.DecisionAdvisor
	if raw.DecisionAdvisorSnake != nil {
		cfg.DecisionAdvisor = *raw.DecisionAdvisorSnake
	}
	cfg.Trajectory = raw.Trajectory
	if raw.Pipeline.MaxWallSeconds < 0 {
		return fmt.Errorf("invalid pipeline.max_wall_seconds %d: must be >= 0", raw.Pipeline.MaxWallSeconds)
	}
	cfg.Pipeline = raw.Pipeline
	cfg.DefaultProjectTrust = raw.DefaultProjectTrust
	cfg.Flags = raw.Flags
	if cfg.MCP.Servers == nil && (len(raw.MCPServers) > 0 || len(raw.MCPServersSnake) > 0) {
		cfg.MCP.Servers = map[string]MCPServerConfig{}
	}
	for name, server := range raw.MCPServers {
		cfg.MCP.Servers[name] = server
	}
	for name, server := range raw.MCPServersSnake {
		if _, exists := cfg.MCP.Servers[name]; exists {
			return fmt.Errorf("MCP server %q is defined in both mcpServers and mcp_servers; mcp_servers would override mcpServers", name)
		}
		cfg.MCP.Servers[name] = server
	}
	return nil
}

func (cfg *LocalControlConfig) UnmarshalJSON(data []byte) error {
	type rawLocalControl struct {
		Enabled      *bool                    `json:"enabled"`
		Browser      LocalControlDriverConfig `json:"browser"`
		Desktop      LocalControlDriverConfig `json:"desktop"`
		Terminal     LocalControlDriverConfig `json:"terminal"`
		ArtifactsDir string                   `json:"artifactsDir"`
	}
	var raw rawLocalControl
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*cfg = LocalControlConfig{
		Browser:      raw.Browser,
		Desktop:      raw.Desktop,
		Terminal:     raw.Terminal,
		ArtifactsDir: raw.ArtifactsDir,
	}
	if raw.Enabled != nil {
		cfg.Enabled = *raw.Enabled
		cfg.enabledSet = true
	}
	return nil
}

func (cfg LocalControlConfig) MarshalJSON() ([]byte, error) {
	type rawLocalControl struct {
		Enabled      *bool                     `json:"enabled,omitempty"`
		Browser      *LocalControlDriverConfig `json:"browser,omitempty"`
		Desktop      *LocalControlDriverConfig `json:"desktop,omitempty"`
		Terminal     *LocalControlDriverConfig `json:"terminal,omitempty"`
		ArtifactsDir string                    `json:"artifactsDir,omitempty"`
	}
	raw := rawLocalControl{
		ArtifactsDir: cfg.ArtifactsDir,
	}
	if cfg.enabledSet || cfg.Enabled {
		raw.Enabled = &cfg.Enabled
	}
	if !cfg.Browser.Empty() {
		raw.Browser = &cfg.Browser
	}
	if !cfg.Desktop.Empty() {
		raw.Desktop = &cfg.Desktop
	}
	if !cfg.Terminal.Empty() {
		raw.Terminal = &cfg.Terminal
	}
	return json.Marshal(raw)
}

func (cfg *LocalControlDriverConfig) UnmarshalJSON(data []byte) error {
	type rawDriver struct {
		Enabled    *bool  `json:"enabled"`
		HelperPath string `json:"helperPath"`
		Driver     string `json:"driver"`
	}
	var raw rawDriver
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*cfg = LocalControlDriverConfig{
		HelperPath: raw.HelperPath,
		Driver:     raw.Driver,
	}
	if raw.Enabled != nil {
		cfg.Enabled = *raw.Enabled
		cfg.enabledSet = true
	}
	return nil
}

func (cfg LocalControlDriverConfig) MarshalJSON() ([]byte, error) {
	type rawDriver struct {
		Enabled    *bool  `json:"enabled,omitempty"`
		HelperPath string `json:"helperPath,omitempty"`
		Driver     string `json:"driver,omitempty"`
	}
	raw := rawDriver{
		HelperPath: cfg.HelperPath,
		Driver:     cfg.Driver,
	}
	if cfg.enabledSet || cfg.Enabled {
		raw.Enabled = &cfg.Enabled
	}
	return json.Marshal(raw)
}

func (server *MCPServerConfig) UnmarshalJSON(data []byte) error {
	type rawServer struct {
		Type     string            `json:"type"`
		Command  string            `json:"command"`
		Args     []string          `json:"args"`
		Env      map[string]string `json:"env"`
		URL      string            `json:"url"`
		Headers  map[string]string `json:"headers"`
		Auth     string            `json:"auth"`
		OAuth    *MCPOAuthConfig   `json:"oauth"`
		Disabled *bool             `json:"disabled"`
	}

	var raw rawServer
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	server.Type = raw.Type
	server.Command = raw.Command
	server.Args = raw.Args
	server.Env = raw.Env
	server.URL = raw.URL
	server.Headers = raw.Headers
	server.Auth = raw.Auth
	server.OAuth = raw.OAuth
	server.Disabled = false
	server.disabledSet = false
	if raw.Disabled != nil {
		server.Disabled = *raw.Disabled
		server.disabledSet = true
	}
	return nil
}

func (profile *ProviderProfile) UnmarshalJSON(data []byte) error {
	type rawProfile struct {
		Name                 string            `json:"name"`
		Provider             string            `json:"provider"`
		ProviderKind         string            `json:"provider_kind"`
		ProviderKindCamel    string            `json:"providerKind"`
		CatalogID            string            `json:"catalogID"`
		CatalogIDSnake       string            `json:"catalog_id"`
		BaseURL              string            `json:"baseURL"`
		BaseURLSnake         string            `json:"base_url"`
		APIKey               string            `json:"apiKey"`
		APIKeySnake          string            `json:"api_key"`
		APIKeyEnv            string            `json:"apiKeyEnv"`
		APIKeyEnvSnake       string            `json:"api_key_env"`
		APIKeyStored         bool              `json:"apiKeyStored"`
		APIKeyStoredSnake    bool              `json:"api_key_stored"`
		APIFormat            string            `json:"apiFormat"`
		APIFormatSnake       string            `json:"api_format"`
		AuthHeader           string            `json:"authHeader"`
		AuthHeaderSnake      string            `json:"auth_header"`
		AuthScheme           string            `json:"authScheme"`
		AuthSchemeSnake      string            `json:"auth_scheme"`
		AuthHeaderValue      string            `json:"authHeaderValue"`
		AuthHeaderValueSnake string            `json:"auth_header_value"`
		CustomHeaders        map[string]string `json:"customHeaders"`
		CustomHeadersSnake   map[string]string `json:"custom_headers"`
		Model                string            `json:"model"`
		ModelID              string            `json:"model_id"`
		ParseThinkTags       *bool             `json:"parseThinkTags"`
		ParseThinkTagsSnake  *bool             `json:"parse_think_tags"`
		Description          string            `json:"description"`
		ReasoningEffort      string            `json:"reasoningEffort"`
		ReasoningEffortSnake string            `json:"reasoning_effort"`
	}

	var raw rawProfile
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	profile.Name = strings.TrimSpace(raw.Name)
	profile.Provider = strings.TrimSpace(raw.Provider)
	profile.ProviderKind = ProviderKind(firstNonEmpty(raw.ProviderKind, raw.ProviderKindCamel, raw.Provider))
	profile.CatalogID = strings.TrimSpace(firstNonEmpty(raw.CatalogID, raw.CatalogIDSnake))
	profile.BaseURL = strings.TrimSpace(firstNonEmpty(raw.BaseURL, raw.BaseURLSnake))
	profile.APIKey = firstNonEmpty(raw.APIKey, raw.APIKeySnake)
	profile.APIKeyEnv = strings.TrimSpace(firstNonEmpty(raw.APIKeyEnv, raw.APIKeyEnvSnake))
	profile.APIKeyStored = raw.APIKeyStored || raw.APIKeyStoredSnake
	profile.APIFormat = strings.TrimSpace(firstNonEmpty(raw.APIFormat, raw.APIFormatSnake))
	profile.AuthHeader = strings.TrimSpace(firstNonEmpty(raw.AuthHeader, raw.AuthHeaderSnake))
	profile.AuthScheme = strings.TrimSpace(firstNonEmpty(raw.AuthScheme, raw.AuthSchemeSnake))
	profile.AuthHeaderValue = firstNonEmpty(raw.AuthHeaderValue, raw.AuthHeaderValueSnake)
	profile.CustomHeaders = firstNonNilMap(raw.CustomHeaders, raw.CustomHeadersSnake)
	profile.Model = strings.TrimSpace(firstNonEmpty(raw.Model, raw.ModelID))
	profile.ParseThinkTags = firstNonNilBool(raw.ParseThinkTags, raw.ParseThinkTagsSnake)
	profile.Description = strings.TrimSpace(raw.Description)
	profile.ReasoningEffort = strings.TrimSpace(firstNonEmpty(raw.ReasoningEffort, raw.ReasoningEffortSnake))
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonNilMap(values ...map[string]string) map[string]string {
	for _, value := range values {
		if value != nil {
			return copyStringMap(value)
		}
	}
	return nil
}

func firstNonNilBool(values ...*bool) *bool {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func copyStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}
