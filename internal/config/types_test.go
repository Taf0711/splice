package config

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSandboxAdditionalReadRootsJSON(t *testing.T) {
	var cfg FileConfig
	if err := json.Unmarshal([]byte(`{"sandbox":{"additionalReadRoots":["/one","/two"]}}`), &cfg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if want := []string{"/one", "/two"}; !reflect.DeepEqual(cfg.Sandbox.AdditionalReadRoots, want) {
		t.Fatalf("AdditionalReadRoots = %v, want %v", cfg.Sandbox.AdditionalReadRoots, want)
	}
}

func TestSandboxAllowReadJSON(t *testing.T) {
	var cfg FileConfig
	if err := json.Unmarshal([]byte(`{"sandbox":{"allowRead":["~/.ssh"]}}`), &cfg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if want := []string{"~/.ssh"}; !reflect.DeepEqual(cfg.Sandbox.AllowRead, want) {
		t.Fatalf("AllowRead = %v, want %v", cfg.Sandbox.AllowRead, want)
	}
}

func TestToolsConfigJSONRoundTrip(t *testing.T) {
	var cfg FileConfig
	if err := json.Unmarshal([]byte(`{"tools":{"deferThreshold":25}}`), &cfg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if cfg.Tools.DeferThreshold != 25 {
		t.Fatalf("Tools.DeferThreshold = %d, want 25", cfg.Tools.DeferThreshold)
	}

	encoded, err := json.Marshal(ToolsConfig{DeferThreshold: 7})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(encoded) != `{"deferThreshold":7}` {
		t.Fatalf("Marshal() = %s, want {\"deferThreshold\":7}", encoded)
	}

	// omitempty: a splice value must not emit the field.
	emptyEncoded, err := json.Marshal(ToolsConfig{})
	if err != nil {
		t.Fatalf("Marshal(empty) error = %v", err)
	}
	if string(emptyEncoded) != `{}` {
		t.Fatalf("Marshal(empty) = %s, want {}", emptyEncoded)
	}
}

func TestToolsConfigPresentOnOverridesAndResolved(t *testing.T) {
	// Compile-time guard that Overrides and ResolvedConfig carry the field too.
	overrides := Overrides{Tools: ToolsConfig{DeferThreshold: 3}}
	resolved := ResolvedConfig{Tools: ToolsConfig{DeferThreshold: 4}}
	if overrides.Tools.DeferThreshold != 3 {
		t.Fatalf("Overrides.Tools.DeferThreshold = %d, want 3", overrides.Tools.DeferThreshold)
	}
	if resolved.Tools.DeferThreshold != 4 {
		t.Fatalf("ResolvedConfig.Tools.DeferThreshold = %d, want 4", resolved.Tools.DeferThreshold)
	}
}

func ptrBool(v bool) *bool { return &v }

func TestWorktreesConfigDefaultsToOn(t *testing.T) {
	var cfg FileConfig
	if err := json.Unmarshal([]byte(`{}`), &cfg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !cfg.Worktrees.EnabledOrDefault() {
		t.Fatal("expected worktrees enabled by default")
	}
	var explicit FileConfig
	if err := json.Unmarshal([]byte(`{"worktrees":{"enabled":false,"directory":"/tmp/wt"}}`), &explicit); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if explicit.Worktrees.EnabledOrDefault() {
		t.Fatal("expected worktrees disabled")
	}
	if explicit.Worktrees.Directory != "/tmp/wt" {
		t.Fatalf("Directory = %q, want /tmp/wt", explicit.Worktrees.Directory)
	}
}

func TestCompactionConfigDefaults(t *testing.T) {
	var cfg FileConfig
	if err := json.Unmarshal([]byte(`{}`), &cfg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !cfg.Compaction.EnabledOrDefault() {
		t.Fatalf("expected compaction enabled by default")
	}
	if !cfg.Compaction.StayInCheapestPricingTierOrDefault() {
		t.Fatalf("expected cheapest pricing tier cap enabled by default")
	}
	if cfg.Compaction.ReserveTokens != 0 {
		t.Fatalf("ReserveTokens = %d, want 0", cfg.Compaction.ReserveTokens)
	}
	if cfg.Compaction.KeepRecentTokens != 0 {
		t.Fatalf("KeepRecentTokens = %d, want 0", cfg.Compaction.KeepRecentTokens)
	}
}

func TestCompactionConfigExplicit(t *testing.T) {
	var cfg FileConfig
	if err := json.Unmarshal([]byte(`{"compaction":{"enabled":false,"reserveTokens":1000,"keepRecentTokens":2000}}`), &cfg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if cfg.Compaction.EnabledOrDefault() {
		t.Fatalf("expected compaction disabled")
	}
	if cfg.Compaction.ReserveTokens != 1000 {
		t.Fatalf("ReserveTokens = %d, want 1000", cfg.Compaction.ReserveTokens)
	}
	if cfg.Compaction.KeepRecentTokens != 2000 {
		t.Fatalf("KeepRecentTokens = %d, want 2000", cfg.Compaction.KeepRecentTokens)
	}

	for name, value := range map[string]bool{"true": true, "false": false} {
		t.Run(name, func(t *testing.T) {
			var explicit FileConfig
			payload := `{"compaction":{"stayInCheapestPricingTier":` + name + `}}`
			if err := json.Unmarshal([]byte(payload), &explicit); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if got := explicit.Compaction.StayInCheapestPricingTierOrDefault(); got != value {
				t.Fatalf("StayInCheapestPricingTierOrDefault() = %v, want %v", got, value)
			}
		})
	}
}

func TestFileConfigMarshalRoundTripCompaction(t *testing.T) {
	cfg := FileConfig{
		Compaction: CompactionConfig{
			Enabled:          ptrBool(true),
			ReserveTokens:    16384,
			KeepRecentTokens: 20000,
		},
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var back FileConfig
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if back.Compaction.Enabled == nil || !*back.Compaction.Enabled {
		t.Fatalf("Enabled did not round-trip")
	}
	if back.Compaction.ReserveTokens != 16384 {
		t.Fatalf("ReserveTokens = %d, want 16384", back.Compaction.ReserveTokens)
	}
	if back.Compaction.KeepRecentTokens != 20000 {
		t.Fatalf("KeepRecentTokens = %d, want 20000", back.Compaction.KeepRecentTokens)
	}
}

func TestCompactionConfigPresentOnResolved(t *testing.T) {
	// Compile-time guard that ResolvedConfig carries the field.
	resolved := ResolvedConfig{Compaction: CompactionConfig{ReserveTokens: 7}}
	if resolved.Compaction.ReserveTokens != 7 {
		t.Fatalf("ResolvedConfig.Compaction.ReserveTokens = %d, want 7", resolved.Compaction.ReserveTokens)
	}
}
