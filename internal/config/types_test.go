package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestVerifyConfigJSONAndValidation(t *testing.T) {
	var cfg FileConfig
	if err := json.Unmarshal([]byte(`{"verify":{"testCommand":["python","tests/runtests.py","{test_modules}"],"testDir":"tests"}}`), &cfg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !reflect.DeepEqual(cfg.Verify.TestCommand, []string{"python", "tests/runtests.py", "{test_modules}"}) || cfg.Verify.TestDir != "tests" {
		t.Fatalf("Verify = %#v", cfg.Verify)
	}
	if err := cfg.Verify.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	for _, dir := range []string{"../outside", "tests/../../outside", "/tmp/tests"} {
		invalid := FileConfig{Verify: VerifyConfig{TestDir: dir}}
		if err := invalid.Verify.Validate(); err == nil || !strings.Contains(err.Error(), "verify.testDir") {
			t.Errorf("Validate(%q) error = %v, want testDir error", dir, err)
		}
	}
}

func TestResolveMergesVerifyFromProjectConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"activeProvider":"openai","providers":[{"name":"openai","provider":"openai","model":"gpt-4o-mini","apiKey":"test-key"}],"verify":{"testCommand":["pytest","{tests}"],"testDir":"tests"}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	resolved, err := Resolve(ResolveOptions{ProjectConfigPath: path, ProjectTrusted: true, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("Resolve() error = %v, resolved = %#v", err, resolved.Verify)
	}
	if !reflect.DeepEqual(resolved.Verify.TestCommand, []string{"pytest", "{tests}"}) || resolved.Verify.TestDir != "tests" {
		t.Fatalf("resolved Verify = %#v", resolved.Verify)
	}
}

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

func TestDecisionBackendConfigLoad(t *testing.T) {
	for _, tc := range []struct {
		name, advisor string
		bad           bool
	}{
		{"default", `{"mode":"shadow","profile":"typesafe/jev-1.13"}`, false},
		{"http", `{"mode":"shadow","backend":"http","profile":"service","endpoint":"https://example.test/decision","limits":{"timeout_ms":100}}`, false},
		{"provider", `{"mode":"shadow","backend":"provider","provider_profile":"openai"}`, false},
		{"unknown", `{"backend":"mystery"}`, true},
		{"http missing endpoint", `{"backend":"http"}`, true},
		{"provider missing profile", `{"backend":"provider"}`, true},
		{"provider unknown profile", `{"backend":"provider","provider_profile":"missing"}`, true},
		{"negative price", `{"limits":{"reservation_usd_per_million_input":-1}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			raw := `{"activeProvider":"openai","providers":[{"name":"openai","provider":"openai","model":"gpt-4o-mini","apiKey":"test-key"}],"decision_advisor":` + tc.advisor + `}`
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			resolved, err := Resolve(ResolveOptions{ProjectConfigPath: path, ProjectTrusted: true, Env: map[string]string{}})
			if (err != nil) != tc.bad {
				t.Fatalf("config=%+v error=%v", resolved.DecisionAdvisor, err)
			}
			if !tc.bad {
				if resolved.DecisionAdvisor.Backend == "" || resolved.DecisionAdvisor.Limits.ReservationUSDPerMillionInput != .042 {
					t.Fatalf("defaults=%+v", resolved.DecisionAdvisor)
				}
				if tc.name == "default" && resolved.DecisionAdvisor.Backend != "openrouter_decisions" {
					t.Fatal("default backend changed")
				}
				if tc.name == "provider" && resolved.DecisionAdvisor.Profile != "gpt-4o-mini" {
					t.Fatal("provider model not resolved")
				}
				encoded, err := json.Marshal(FileConfig{DecisionAdvisor: resolved.DecisionAdvisor})
				if err != nil {
					t.Fatal(err)
				}
				var decoded FileConfig
				if err := json.Unmarshal(encoded, &decoded); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(decoded.DecisionAdvisor, resolved.DecisionAdvisor) {
					t.Fatal("advisor JSON round trip lost fields")
				}
			}
		})
	}
}
func TestTrajectoryThresholdConfigLoad(t *testing.T) {
	for _, thresholds := range []string{`{"step_back":0.9}`, `{"stop":1.1}`, `{"unknown":0.5}`} {
		path := filepath.Join(t.TempDir(), "config.json")
		raw := `{"activeProvider":"openai","providers":[{"name":"openai","provider":"openai","model":"gpt-4o-mini","apiKey":"test-key"}],"trajectory":{"thresholds":` + thresholds + `}}`
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		resolved, err := Resolve(ResolveOptions{ProjectConfigPath: path, ProjectTrusted: true, Env: map[string]string{}})
		if thresholds == `{"step_back":0.9}` {
			if err != nil || resolved.Trajectory.Thresholds["step_back"] != .9 {
				t.Fatalf("thresholds=%+v error=%v", resolved.Trajectory, err)
			}
			encoded, _ := json.Marshal(FileConfig{Trajectory: resolved.Trajectory})
			var decoded FileConfig
			if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.Trajectory.Thresholds["step_back"] != .9 {
				t.Fatal("threshold round trip lost")
			}
		} else if err == nil {
			t.Fatalf("invalid thresholds accepted: %s", thresholds)
		}
	}
}

// AUDIT-06: the pipeline wall-time limit comes from SPLICE_MAX_WALL, then
// pipeline.max_wall_seconds in the user config, then the default (zero).
func TestResolveMaxWallPrecedence(t *testing.T) {
	provider := `"activeProvider":"openai","providers":[{"name":"openai","provider":"openai","model":"gpt-4o-mini","apiKey":"test-key"}]`
	write := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	withConfig := write(t, `{`+provider+`,"pipeline":{"max_wall_seconds":1800}}`)
	without := write(t, `{`+provider+`}`)

	tests := []struct {
		name    string
		path    string
		env     string
		want    time.Duration
		wantErr string
	}{
		{name: "default", path: without, want: 0},
		{name: "config", path: withConfig, want: 30 * time.Minute},
		{name: "env over config", path: withConfig, env: "45m", want: 45 * time.Minute},
		{name: "env without config", path: without, env: "90s", want: 90 * time.Second},
		{name: "env unparsable", path: withConfig, env: "soon", wantErr: MaxWallEnv},
		{name: "env zero", path: withConfig, env: "0s", wantErr: MaxWallEnv},
		{name: "env negative", path: withConfig, env: "-5m", wantErr: MaxWallEnv},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			if tc.env != "" {
				env[MaxWallEnv] = tc.env
			}
			resolved, err := Resolve(ResolveOptions{UserConfigPath: tc.path, Env: env})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("Resolve() error = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if resolved.MaxWall != tc.want {
				t.Fatalf("MaxWall = %v, want %v", resolved.MaxWall, tc.want)
			}
		})
	}

	// A trusted project config cannot set the limit: the user value, or the
	// default, stands.
	project := write(t, `{"pipeline":{"max_wall_seconds":7200}}`)
	for _, tc := range []struct {
		user string
		want time.Duration
	}{{withConfig, 30 * time.Minute}, {without, 0}} {
		resolved, err := Resolve(ResolveOptions{UserConfigPath: tc.user, ProjectConfigPath: project, ProjectTrusted: true, Env: map[string]string{}})
		if err != nil {
			t.Fatalf("Resolve() with a project limit error = %v", err)
		}
		if resolved.MaxWall != tc.want {
			t.Fatalf("MaxWall with a project limit = %v, want %v", resolved.MaxWall, tc.want)
		}
	}

	// A value that overflows time.Duration is refused, also when the env sets
	// the limit, instead of wrapping to a wrong limit.
	huge := write(t, fmt.Sprintf(`{%s,"pipeline":{"max_wall_seconds":%d}}`, provider, maxWallSecondsLimit+1))
	for _, env := range []map[string]string{{}, {MaxWallEnv: "30m"}} {
		if _, err := Resolve(ResolveOptions{UserConfigPath: huge, Env: env}); err == nil || !strings.Contains(err.Error(), "invalid pipeline.max_wall_seconds") {
			t.Fatalf("Resolve() with an overflowing limit (env %v) error = %v, want a named config error", env, err)
		}
	}
	atLimit := write(t, fmt.Sprintf(`{%s,"pipeline":{"max_wall_seconds":%d}}`, provider, maxWallSecondsLimit))
	if resolved, err := Resolve(ResolveOptions{UserConfigPath: atLimit, Env: map[string]string{}}); err != nil || resolved.MaxWall <= 0 {
		t.Fatalf("Resolve() at the limit = %v, %v; want a positive limit", resolved.MaxWall, err)
	}

	var negative FileConfig
	if err := json.Unmarshal([]byte(`{"pipeline":{"max_wall_seconds":-1}}`), &negative); err == nil || !strings.Contains(err.Error(), "pipeline.max_wall_seconds") {
		t.Fatalf("negative max_wall_seconds error = %v", err)
	}
	encoded, err := json.Marshal(FileConfig{Pipeline: PipelineConfig{MaxWallSeconds: 1800}})
	if err != nil {
		t.Fatal(err)
	}
	var decoded FileConfig
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.Pipeline.MaxWallSeconds != 1800 {
		t.Fatalf("round trip = %s, %+v, %v", encoded, decoded.Pipeline, err)
	}
}
