package modelregistry

import "testing"

func mkEntry(id, alias string) ModelEntry {
	return ModelEntry{
		ID: id, DisplayName: id, APIModel: id, Provider: ProviderAnthropic,
		ContextLimits: ContextLimits{ContextWindow: 200000, MaxOutputTokens: 64000},
		Capabilities:  ModelCapabilities{ModelCapabilityChat},
		Status:        ModelStatusActive, Aliases: []string{alias},
		Cost: ModelCost{
			Currency: "USD", Unit: "per_1m_tokens",
			InputPerMillion: 1, OutputPerMillion: 2,
			Source: "test", SourceLastVerified: "2026-06-06",
		},
	}
}

func resolveTestRegistry(t *testing.T) Registry {
	t.Helper()
	sonnet := mkEntry("claude-sonnet-4-5", "sonnet-4.5")
	sonnet.MatchPatterns = []string{`(?i)sonnet[^a-z0-9]*4[.\s]?5`}
	sonnet.ReasoningEfforts = []ReasoningEffort{ReasoningEffortNone, ReasoningEffortLow, ReasoningEffortHigh}
	sonnet.DefaultReasoningEffort = ReasoningEffortLow

	old := mkEntry("claude-sonnet-4-0", "sonnet-4.0")
	old.Status = ModelStatusDeprecated
	old.Deprecation = &DeprecationRule{FallbackID: "claude-sonnet-4-5", WarningMsg: "sonnet-4-0 retired; use 4.5"}

	reg, err := NewRegistry([]ModelEntry{sonnet, old})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestResolveRegexAlias(t *testing.T) {
	reg := resolveTestRegistry(t)
	for _, in := range []string{"claude-sonnet-4-5", "sonnet-4.5", "Sonnet 4.5", "sonnet4.5"} {
		m, ok := reg.Resolve(in)
		if !ok || m.ID != "claude-sonnet-4-5" {
			t.Errorf("Resolve(%q) = %q,%v; want claude-sonnet-4-5", in, m.ID, ok)
		}
	}
	if _, ok := reg.Resolve("totally-unknown"); ok {
		t.Error("unknown input should not resolve")
	}
}

func TestResolveWithFallbackRedirectsDeprecated(t *testing.T) {
	reg := resolveTestRegistry(t)
	m, notice, ok := reg.ResolveWithFallback("claude-sonnet-4-0")
	if !ok || m.ID != "claude-sonnet-4-5" {
		t.Fatalf("expected redirect to 4.5, got %q,%v", m.ID, ok)
	}
	if notice == "" {
		t.Error("expected a deprecation notice")
	}
}

func TestResolveWithFallbackActiveNoNotice(t *testing.T) {
	reg := resolveTestRegistry(t)
	m, notice, ok := reg.ResolveWithFallback("Sonnet 4.5")
	if !ok || m.ID != "claude-sonnet-4-5" || notice != "" {
		t.Fatalf("active model should resolve cleanly, got %q notice=%q", m.ID, notice)
	}
}

func TestEffectiveReasoningEffort(t *testing.T) {
	reg := resolveTestRegistry(t)
	m, _ := reg.Get("claude-sonnet-4-5")
	if got := EffectiveReasoningEffort(m, ReasoningEffortHigh); got != ReasoningEffortHigh {
		t.Errorf("supported effort = %q; want high", got)
	}
	if got := EffectiveReasoningEffort(m, ReasoningEffortXHigh); got != ReasoningEffortHigh {
		t.Errorf("unsupported effort should clamp to nearest supported high, got %q", got)
	}
	if got := EffectiveReasoningEffort(m, ""); got != ReasoningEffortLow {
		t.Errorf("empty effort should use default low, got %q", got)
	}
}

func TestDefaultRegistryReasoningEffortTiers(t *testing.T) {
	registry, err := DefaultRegistry()
	if err != nil {
		t.Fatalf("DefaultRegistry: %v", err)
	}
	luna, err := registry.Require("gpt-5.6-luna")
	if err != nil {
		t.Fatalf("Require gpt-5.6-luna: %v", err)
	}
	sonnet, err := registry.Require("claude-sonnet-4.5")
	if err != nil {
		t.Fatalf("Require claude-sonnet-4.5: %v", err)
	}
	gemini, err := registry.Require("gemini-2.5-pro")
	if err != nil {
		t.Fatalf("Require gemini-2.5-pro: %v", err)
	}
	gpt41, err := registry.Require("gpt-4.1")
	if err != nil {
		t.Fatalf("Require gpt-4.1: %v", err)
	}

	cases := []struct {
		name      string
		model     ModelEntry
		requested ReasoningEffort
		want      ReasoningEffort
	}{
		{"luna xhigh", luna, ReasoningEffortXHigh, ReasoningEffortXHigh},
		{"luna max", luna, ReasoningEffortMax, ReasoningEffortMax},
		{"luna default", luna, "", ReasoningEffortMedium},
		{"sonnet xhigh", sonnet, ReasoningEffortXHigh, ReasoningEffortHigh},
		{"gemini max", gemini, ReasoningEffortMax, ReasoningEffortHigh},
		{"gpt-4.1 high", gpt41, ReasoningEffortHigh, ReasoningEffortNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := EffectiveReasoningEffort(tc.model, tc.requested); got != tc.want {
				t.Fatalf("EffectiveReasoningEffort(%q, %q) = %q, want %q", tc.model.ID, tc.requested, got, tc.want)
			}
		})
	}

	efforts := registry.ReasoningEfforts("gpt-5.6-luna")
	if !containsReasoningEffort(efforts, ReasoningEffortXHigh) || !containsReasoningEffort(efforts, ReasoningEffortMax) {
		t.Fatalf("gpt-5.6-luna efforts = %v, want xhigh and max", efforts)
	}
	if containsReasoningEffort(efforts, ReasoningEffortMinimal) {
		t.Fatalf("gpt-5.6-luna efforts = %v, must not contain minimal", efforts)
	}
}

func containsReasoningEffort(efforts []ReasoningEffort, want ReasoningEffort) bool {
	for _, effort := range efforts {
		if effort == want {
			return true
		}
	}
	return false
}

// TestEffectiveReasoningEffortUsesNameFallback pins that the run-time resolver
// honors the same name-based fallback the /effort picker uses. A model that the
// catalog enumerates with no efforts but whose name is a known reasoning family
// (e.g. a GPT-5 variant served via a proxy) must have its requested effort
// honored — not silently coerced to "none" while the picker advertises controls.
func TestEffectiveReasoningEffortUsesNameFallback(t *testing.T) {
	// Registered model, no explicit efforts, GPT-5 api model -> fallback infers
	// {minimal, low, medium, high}.
	gpt5 := ModelEntry{ID: "gpt-5-proxy", APIModel: "gpt-5", Provider: ProviderOpenAI}
	if got := EffectiveReasoningEffort(gpt5, ReasoningEffortHigh); got != ReasoningEffortHigh {
		t.Errorf("supported (via name fallback) effort = %q; want high", got)
	}
	if got := EffectiveReasoningEffort(gpt5, ReasoningEffortMinimal); got != ReasoningEffortMinimal {
		t.Errorf("minimal (via name fallback) = %q; want minimal", got)
	}
	// xhigh is outside the inferred set and there is no declared default, so it
	// clamps downward to the nearest inferred tier.
	if got := EffectiveReasoningEffort(gpt5, ReasoningEffortXHigh); got != ReasoningEffortHigh {
		t.Errorf("unsupported effort on a fallback model = %q; want high", got)
	}

	// Non-reasoning model: name matches nothing, stays "none".
	gpt4o := ModelEntry{ID: "gpt-4o", APIModel: "gpt-4o", Provider: ProviderOpenAI}
	if got := EffectiveReasoningEffort(gpt4o, ReasoningEffortHigh); got != ReasoningEffortNone {
		t.Errorf("non-reasoning model = %q; want none", got)
	}

	// The picker and the resolver must agree on the supported set for the same id.
	reg := resolveTestRegistry(t)
	picker := reg.ReasoningEfforts("gpt-5") // unknown -> name fallback
	if len(picker) == 0 {
		t.Fatal("picker should advertise efforts for a gpt-5 name")
	}
	for _, tier := range picker {
		if got := EffectiveReasoningEffort(gpt5, tier); got != tier {
			t.Errorf("picker advertises %q but resolver returns %q", tier, got)
		}
	}
}
