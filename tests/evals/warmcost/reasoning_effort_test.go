package warmcost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// glmCatalogBody is the shape OpenRouter served for z-ai/glm-5.3-flash on
// 2026-09-17: it advertises max/high/low only. The fam-05 automatic arm pinned
// medium, the provider substituted high, and the arm failed to submit.
const glmCatalogBody = `{
  "data": [
    {"id": "some/other", "reasoning": {"supported_efforts": ["low"], "default_effort": "low"}},
    {"id": "z-ai/glm-5.3-flash", "reasoning": {"mandatory": true, "default_enabled": true,
      "supported_efforts": ["max", "high", "low"], "default_effort": "max"}}
  ]
}`

func TestParseModelReasoningInfoAndEffortSupport(t *testing.T) {
	info, err := ParseModelReasoningInfo([]byte(glmCatalogBody), "z-ai/glm-5.3-flash")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if strings.Join(info.SupportedEfforts, ",") != "max,high,low" || info.DefaultEffort != "max" {
		t.Fatalf("info = %+v, want max/high/low default max", info)
	}
	// The recorded defect: the pinned medium is NOT advertised, so the guard
	// must reject it. high and the provider default are accepted.
	if EffortSupported("medium", info.SupportedEfforts) {
		t.Fatal("medium reported supported for a model that advertises only max/high/low")
	}
	if !EffortSupported("high", info.SupportedEfforts) || !EffortSupported("HIGH", info.SupportedEfforts) {
		t.Fatal("high must be supported (case-insensitive)")
	}
	if !EffortSupported("", info.SupportedEfforts) {
		t.Fatal("empty effort is the provider default and must not be rejected")
	}
}

func TestParseModelReasoningInfoErrors(t *testing.T) {
	if _, err := ParseModelReasoningInfo([]byte(glmCatalogBody), "absent/model"); err == nil {
		t.Fatal("absent model must be a loud error, not a silent pass")
	}
	noReasoning := `{"data":[{"id":"plain/model"}]}`
	if _, err := ParseModelReasoningInfo([]byte(noReasoning), "plain/model"); err == nil {
		t.Fatal("model without reasoning metadata must be a loud error")
	}
	if _, err := ParseModelReasoningInfo([]byte("not json"), "z-ai/glm-5.3-flash"); err == nil {
		t.Fatal("undecodable catalog must be an error")
	}
}

func TestFetchModelReasoningInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(glmCatalogBody))
	}))
	defer srv.Close()
	info, err := FetchModelReasoningInfo(context.Background(), srv.URL, "z-ai/glm-5.3-flash", srv.Client())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if EffortSupported("medium", info.SupportedEfforts) {
		t.Fatal("medium must be rejected from the fetched catalog")
	}
}

func TestResolveStageReasoningEffort(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir,
		`{"activeProvider":"openrouter","providers":[{"name":"openrouter","model":"z-ai/glm-5.3-flash","reasoningEffort":"medium","active":true}]}`,
		`{"default":{"provider_profile":"openrouter","model":"z-ai/glm-5.3-flash","reasoning_effort":"medium"},
		  "stages":{"code_writer":{"provider_profile":"openrouter","model":"z-ai/glm-5.3-flash","reasoning_effort":"medium"}}}`)
	got := ResolveStageReasoningEffort(dir, "code_writer")
	if got.Effort != "medium" || got.Source != "stage-models.json:code_writer" {
		t.Fatalf("resolved %+v, want the code_writer medium override surfaced", got)
	}

	// No stage file: the active provider's reasoningEffort is the source.
	dir2 := t.TempDir()
	writeConfig(t, dir2,
		`{"activeProvider":"openrouter","providers":[{"name":"openrouter","model":"z-ai/glm-5.3-flash","reasoningEffort":"high","active":true}]}`,
		"")
	got2 := ResolveStageReasoningEffort(dir2, "code_writer")
	if got2.Effort != "high" || got2.Source != "config.json:activeProvider" {
		t.Fatalf("resolved %+v, want the provider high effort", got2)
	}

	// Nothing configured: empty effort, explicitly unresolved.
	dir3 := t.TempDir()
	got3 := ResolveStageReasoningEffort(dir3, "code_writer")
	if got3.Effort != "" || got3.Source != "unresolved" {
		t.Fatalf("resolved %+v, want unresolved empty effort", got3)
	}
}
