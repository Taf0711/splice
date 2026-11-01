package splice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
)

// synthetic secret for redaction tests. It matches the api_key pattern in the
// redaction package.
const syntheticSecret = "api_key=sk-SYNTHETIC-0123456789"

func TestEventSinkBracketsRunWithLifecycleAndStageEvents(t *testing.T) {
	workDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workDir, "go.mod"), []byte("module example\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreTools(workDir) {
		registry.Register(tool)
	}
	var got []agent.StageEvent
	_, err := Run(context.Background(), "add a Hello function and tests", runFakeProvider{}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		EventSink:      func(ev agent.StageEvent) { got = append(got, ev) },
	}, &exemplarStore{}, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(got) < 3 {
		t.Fatalf("sink received %d events, want run start, stage events, and run end", len(got))
	}
	first, last := got[0], got[len(got)-1]
	if first.Kind != "run" || first.Status != "running" {
		t.Errorf("first event = %+v, want run start", first)
	}
	if last.Kind != "run" || last.Status != "completed" {
		t.Errorf("last event = %+v, want run end completed", last)
	}
	var stages int
	for _, ev := range got[1 : len(got)-1] {
		if ev.Kind == "" && ev.Name != "" {
			stages++
		}
	}
	if stages == 0 {
		t.Fatal("sink received no stage events between run start and end")
	}
}

func TestEmitSinkRedactsDetailAndLeavesOtherPathsRaw(t *testing.T) {
	var sinkEvents []agent.StageEvent
	var rawStage []agent.StageEvent
	var rawNotices []string
	options := PipelineRunConfig{
		EventSink:    func(ev agent.StageEvent) { sinkEvents = append(sinkEvents, ev) },
		OnStageEvent: func(ev agent.StageEvent) { rawStage = append(rawStage, ev) },
		OnNotice:     func(text string) { rawNotices = append(rawNotices, text) },
	}

	emitStageEvent(options, 0, "code_writer", "completed", "wrote files with "+syntheticSecret, 100, nil)
	emitNotice(options, "[topology] note "+syntheticSecret)

	if len(sinkEvents) != 2 {
		t.Fatalf("sink received %d events, want stage and notice", len(sinkEvents))
	}
	stage, notice := sinkEvents[0], sinkEvents[1]
	if stage.Kind != "" || stage.Name != "code_writer" {
		t.Errorf("stage sink event = %+v", stage)
	}
	if strings.Contains(stage.Detail, "SYNTHETIC") || !strings.Contains(stage.Detail, "[REDACTED]") {
		t.Errorf("stage detail reached the sink unredacted: %q", stage.Detail)
	}
	if notice.Kind != "notice" || strings.Contains(notice.Detail, "SYNTHETIC") || !strings.Contains(notice.Detail, "[REDACTED]") {
		t.Errorf("notice sink event = %+v, want redacted notice", notice)
	}

	// Non-sink paths keep the original text.
	if len(rawStage) != 1 || rawStage[0].Detail != "wrote files with "+syntheticSecret {
		t.Errorf("OnStageEvent detail changed: %+v", rawStage)
	}
	if len(rawNotices) != 1 || rawNotices[0] != "[topology] note "+syntheticSecret {
		t.Errorf("OnNotice text changed: %q", rawNotices)
	}
}

// emitSequence runs the emit helpers that a stage and a notice use, and returns
// every observable output: the reasoning stream (with the \x00STAGE markers),
// the stage events, and the notices.
func emitSequence(sink func(agent.StageEvent)) (reasoning string, stages []agent.StageEvent, notices []string) {
	var out strings.Builder
	options := PipelineRunConfig{
		EventSink:    sink,
		OnReasoning:  func(text string) { out.WriteString(text) },
		OnStageEvent: func(ev agent.StageEvent) { stages = append(stages, ev) },
		OnNotice:     func(text string) { notices = append(notices, text) },
	}
	emitStageEvent(options, 0, "code_writer", "running", "start", 0, nil)
	emitStageEvent(options, 0, "code_writer", "completed", "done", 100, []string{"main.go"})
	emitNotice(options, "[topology] notice")
	return out.String(), stages, notices
}

// TestNilEventSinkIsSafe pins the no-behavior-change rule: a sink changes nothing
// that the existing callbacks see, and a nil sink changes nothing either.
func TestNilEventSinkIsSafe(t *testing.T) {
	withoutReasoning, withoutStages, withoutNotices := emitSequence(nil)
	var sinkEvents []agent.StageEvent
	withReasoning, withStages, withNotices := emitSequence(func(ev agent.StageEvent) { sinkEvents = append(sinkEvents, ev) })

	if withoutReasoning != withReasoning {
		t.Fatalf("reasoning stream differs with a sink:\nnil:  %q\nsink: %q", withoutReasoning, withReasoning)
	}
	if !strings.Contains(withReasoning, stageEventMarkerBegin) {
		t.Fatalf("reasoning stream lost its \\x00STAGE markers: %q", withReasoning)
	}
	if fmt.Sprint(withoutStages) != fmt.Sprint(withStages) {
		t.Fatalf("stage events differ with a sink:\nnil:  %+v\nsink: %+v", withoutStages, withStages)
	}
	if fmt.Sprint(withoutNotices) != fmt.Sprint(withNotices) {
		t.Fatalf("notices differ with a sink:\nnil:  %q\nsink: %q", withoutNotices, withNotices)
	}
	if len(sinkEvents) != 3 {
		t.Fatalf("sink received %d events, want 2 stage events and 1 notice", len(sinkEvents))
	}
	// A nil sink, called directly, does nothing and does not panic.
	emitSink(nil, nil, agent.StageEvent{Kind: "run"})
}

func TestEmitSinkRedactsConfiguredSecretValues(t *testing.T) {
	var sinkEvents []agent.StageEvent
	options := PipelineConfigFromAgentOptions(agent.Options{
		EventSink:    func(ev agent.StageEvent) { sinkEvents = append(sinkEvents, ev) },
		SecretValues: []string{tkBare},
	})
	// tkBare matches no redaction pattern, so only the exact-value list can redact it.
	emitStageEvent(options, 0, "code_writer", "running", "calling the provider with "+tkBare, 0, nil)
	if len(sinkEvents) != 1 {
		t.Fatalf("sink received %d events, want 1", len(sinkEvents))
	}
	if strings.Contains(sinkEvents[0].Detail, tkBare) || !strings.Contains(sinkEvents[0].Detail, "[REDACTED]") {
		t.Fatalf("configured key reached the sink: %q", sinkEvents[0].Detail)
	}
}

// TestEmitSinkRedactsNodeNameAndFilePaths: a secret in a node name or in a
// changed file path is redacted on the sink, as the viewer redacts it. The
// raw stage callback keeps the original values.
func TestEmitSinkRedactsNodeNameAndFilePaths(t *testing.T) {
	var sinkEvents, rawStage []agent.StageEvent
	options := PipelineConfigFromAgentOptions(agent.Options{
		EventSink:    func(ev agent.StageEvent) { sinkEvents = append(sinkEvents, ev) },
		OnStageEvent: func(ev agent.StageEvent) { rawStage = append(rawStage, ev) },
		SecretValues: []string{tkBare},
	})
	emitStageEvent(options, 0, "node_"+tkBare, "completed", "done", 100, []string{"keys/" + tkBare + ".txt", "main.go"})
	if len(sinkEvents) != 1 {
		t.Fatalf("sink received %d events, want 1", len(sinkEvents))
	}
	got := sinkEvents[0]
	if strings.Contains(got.Name, tkBare) || strings.Contains(strings.Join(got.ChangedFiles, " "), tkBare) {
		t.Fatalf("secret reached the sink: name=%q files=%q", got.Name, got.ChangedFiles)
	}
	if len(got.ChangedFiles) != 2 || got.ChangedFiles[1] != "main.go" {
		t.Fatalf("sink files = %q, want two entries with main.go kept", got.ChangedFiles)
	}
	if len(rawStage) != 1 || rawStage[0].Name != "node_"+tkBare || rawStage[0].ChangedFiles[0] != "keys/"+tkBare+".txt" {
		t.Fatalf("OnStageEvent changed: %+v", rawStage)
	}
}

func TestBracketRunEndsWithRedactedFailure(t *testing.T) {
	var got []agent.StageEvent
	_, err := bracketRun(agent.Options{
		EventSink:    func(ev agent.StageEvent) { got = append(got, ev) },
		SecretValues: []string{tkBare},
	}, func() (agent.Result, error) {
		return agent.Result{}, errors.New("provider rejected " + tkBare)
	})
	if err == nil {
		t.Fatal("bracketRun must return the run error")
	}
	if len(got) != 2 || got[0].Status != "running" || got[1].Status != "failed" {
		t.Fatalf("events = %+v, want running then failed", got)
	}
	if strings.Contains(got[1].Detail, tkBare) || !strings.Contains(got[1].Detail, "[REDACTED]") {
		t.Fatalf("failure detail reached the sink unredacted: %q", got[1].Detail)
	}
}

func TestRunDesignPlanIsBracketedAtTheSink(t *testing.T) {
	workDir, registry := newRunTestWorkspace(t)
	plan := designPlan([]schemas.Task{designTask("t1", "First", nil)})
	var got []agent.StageEvent
	_, err := RunDesignPlan(context.Background(), plan, designHappyProvider{workDir: workDir}, agent.Options{
		Cwd:            workDir,
		Registry:       registry,
		PermissionMode: agent.PermissionModeAuto,
		SessionID:      "plan-sink",
		MaxTurns:       1,
		FileTracker:    tools.NewFileTracker(),
		EventSink:      func(ev agent.StageEvent) { got = append(got, ev) },
	}, nil, nil)
	if err != nil {
		t.Fatalf("RunDesignPlan: %v", err)
	}
	if len(got) < 3 {
		t.Fatalf("sink received %d events, want run start, stage events, and run end", len(got))
	}
	if first := got[0]; first.Kind != "run" || first.Status != "running" {
		t.Errorf("first event = %+v, want run start", first)
	}
	if last := got[len(got)-1]; last.Kind != "run" || last.Status != "completed" {
		t.Errorf("last event = %+v, want run end completed", last)
	}
}

// tkBare and the keys below match no redaction pattern, so a test that sees them
// redacted proves the exact-value list did it.
const (
	tkBare         = "zq7Lm2Vt9Rp4Xw8K"
	tkPrimary      = "Hd4Jk9Qw2Ev7Nr1B"
	tkStage        = "rT5kQp8Zw3Lm6Vn2"
	tkEscalate     = "Yc3Bn7Mx1Gs8Dk4F"
	tkAuthHeader   = "Lp6Vq2Zr9Tn3Wk7X"
	tkCustomHeader = "Uj8Fh4Ng1Sb5Cd0M"
)

func TestRunSecretValuesCoversEveryRouteProfile(t *testing.T) {
	primary := config.ProviderProfile{Name: "main", APIKey: tkPrimary}
	profiles := []config.ProviderProfile{
		{Name: "main", APIKey: tkPrimary}, // same key again: deduplicated
		{Name: "stage", APIKey: tkStage},
		{Name: "escalation", APIKey: tkEscalate},
		{Name: "unset", APIKey: ""}, // empty: skipped
	}
	got := RunSecretValues(primary, profiles)
	want := []string{tkPrimary, tkStage, tkEscalate}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("RunSecretValues = %q, want %q", got, want)
	}
}

func TestRunSecretValuesIncludesHeaderValues(t *testing.T) {
	primary := config.ProviderProfile{
		Name:            "main",
		APIKey:          tkPrimary,
		AuthHeaderValue: tkAuthHeader,
		CustomHeaders:   map[string]string{"X-Team": tkCustomHeader, "X-Empty": ""},
	}
	got := RunSecretValues(primary, nil)
	want := []string{tkPrimary, tkAuthHeader, tkCustomHeader}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("RunSecretValues = %q, want %q", got, want)
	}
}

func TestSinkRedactsStageRouteKeyThatDiffersFromPrimary(t *testing.T) {
	var sinkEvents []agent.StageEvent
	options := PipelineConfigFromAgentOptions(agent.Options{
		EventSink: func(ev agent.StageEvent) { sinkEvents = append(sinkEvents, ev) },
		SecretValues: RunSecretValues(
			config.ProviderProfile{Name: "main", APIKey: tkPrimary},
			[]config.ProviderProfile{{Name: "stage", APIKey: tkStage}},
		),
	})
	emitStageEvent(options, 0, "code_writer", "running", "routed through "+tkStage, 0, nil)
	if len(sinkEvents) != 1 {
		t.Fatalf("sink received %d events, want 1", len(sinkEvents))
	}
	if strings.Contains(sinkEvents[0].Detail, tkStage) || !strings.Contains(sinkEvents[0].Detail, "[REDACTED]") {
		t.Fatalf("stage route key reached the sink: %q", sinkEvents[0].Detail)
	}
}
