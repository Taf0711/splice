package tui

// P13/P14 verification-pass probes (Pen frames wxhww cells 3/4 and A7nbsQ
// cells 11/12). Each probe pins one frame cell through the real Update/render
// path with ansi.Strip assertions, per the audit's wire-as-you-go rule.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/providerhealth"
	"github.com/Taf0711/splice/internal/sessions"
)

// stripCardPrefix removes the NUL command-card routing tag so assertions run
// against visible text only.
func stripCardPrefix(t *testing.T, rows []transcriptRow) string {
	t.Helper()
	return strings.ReplaceAll(transcriptText(rows), commandCardTranscriptPrefix, "")
}

// P13 cell 3 (/context): the runtime card carries cwd, provider, model,
// effort, style, tools, plus pointers to /permissions and /tools, and is
// read-only (no agent run starts).
func TestContextCardCarriesStyleToolsAndActionPointers(t *testing.T) {
	m := newModel(context.Background(), Options{
		Cwd:          "/tmp/probe",
		ProviderName: "anthropic",
		ModelName:    "claude-opus-5",
		ProviderProfile: config.ProviderProfile{
			Name:  "anthropic",
			Model: "claude-opus-5",
		},
	})
	m.input.SetValue("/context")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("/context must be read-only, got an agent command")
	}
	text := ansi.Strip(transcriptText(next.transcript))
	for _, want := range []string{
		"Context",
		"cwd",
		"provider   anthropic",
		"model      claude-opus-5",
		"effort",
		"style",
		"registered  0",
		"actions: /permissions manage access | /tools inspect catalog",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("context card missing %q:\n%s", want, text)
		}
	}
}

// P13 cell 4 (/doctor): the bare command pins the five real checks from
// internal/doctor and the FIX guidance lines from the frame. The sandbox
// platform and PATH lookups are pinned so the check set is deterministic.
func TestDoctorCardPinsFiveChecksAndFixForms(t *testing.T) {
	m := newModel(context.Background(), Options{
		UserConfigPath: "C:/splice/user.json",
		DoctorGOOS:     "darwin",
		DoctorLookupExecutable: func(name string) (string, error) {
			if name == "sandbox-exec" || name == "gopls" {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("executable file not found in $PATH")
		},
		ProviderProfile: config.ProviderProfile{
			Name:         "custom",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "https://api.example.com/v1",
			Model:        "custom-model",
		},
	})
	m.input.SetValue("/doctor")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("expected plain /doctor to render synchronously")
	}
	text := ansi.Strip(transcriptText(next.transcript))
	for _, want := range []string{
		"provider.config",
		"provider.model",
		"provider.connectivity",
		"sandbox.backend",
		"lsp.servers",
		"/doctor fix",
		"/doctor --connectivity",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("doctor card missing %q:\n%s", want, text)
		}
	}
}

// P13 cell 4 (/doctor fix): with a credentialed provider and a healthy probe,
// /doctor fix re-probes connectivity (the second FIX form).
func TestDoctorFixReprobesWhenConfigured(t *testing.T) {
	profile := config.ProviderProfile{
		Name:         "custom",
		ProviderKind: config.ProviderKindOpenAICompatible,
		BaseURL:      "https://api.example.com/v1",
		Model:        "custom-model",
		APIKey:       "sk-test", // credentialed so provider.config passes and /doctor fix re-probes
	}
	calls := 0
	m := newModel(context.Background(), Options{
		ProviderProfile: profile,
		ProbeProviderHealth: func(context.Context, providerhealth.Options) providerhealth.Result {
			calls++
			return providerhealth.Result{
				Status: providerhealth.StatusPass,
				Checks: []providerhealth.Check{{
					ID:      "provider.connectivity",
					Status:  providerhealth.StatusPass,
					Message: "reachable",
				}},
			}
		},
	})
	m.input.SetValue("/doctor fix")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected /doctor fix to re-probe asynchronously")
	}
	msg := execCmd(cmd)
	if msg == nil {
		t.Fatal("expected the async re-probe command to return a message")
	}
	updated, _ = next.Update(msg)
	final := updated.(model)
	if calls != 1 {
		t.Fatalf("expected exactly one connectivity probe round-trip, got %d", calls)
	}
	if !transcriptContains(final.transcript, "[pass] provider.connectivity") {
		t.Fatalf("expected re-probe result in transcript, got %#v", final.transcript)
	}
}

// P13 cell 4 (/doctor --connectivity): exactly one live round-trip.
func TestDoctorConnectivityDoesOneRoundTrip(t *testing.T) {
	profile := config.ProviderProfile{
		Name:         "custom",
		ProviderKind: config.ProviderKindOpenAICompatible,
		BaseURL:      "https://api.example.com/v1",
		Model:        "custom-model",
	}
	calls := 0
	m := newModel(context.Background(), Options{
		ProviderProfile: profile,
		ProbeProviderHealth: func(context.Context, providerhealth.Options) providerhealth.Result {
			calls++
			return providerhealth.Result{
				Status: providerhealth.StatusPass,
				Checks: []providerhealth.Check{{
					ID:      "provider.connectivity",
					Status:  providerhealth.StatusPass,
					Message: "reachable",
				}},
			}
		},
	})
	m.input.SetValue("/doctor --connectivity")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("expected /doctor --connectivity to probe asynchronously")
	}
	msg := execCmd(cmd)
	updated, _ = next.Update(msg)
	if calls != 1 {
		t.Fatalf("expected one live round-trip, got %d", calls)
	}
}

// P14 cell 11 (/compact status): the status card shows % of context window,
// the compactable-turn count, the last result (honest "none this session"
// before any compaction), and the now action with what it preserves.
func TestCompactStatusCardCarriesFillTurnsResultAndAction(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir()})
	m := newModel(context.Background(), Options{
		ModelName:    "gpt-4.1",
		SessionStore: store,
	})
	var err error
	m, err = m.ensureActiveSession("fill the session")
	if err != nil {
		t.Fatal(err)
	}
	// tuiCompactionPreserveLast = 8, so 12 events leave 4 compactable turns
	// older than the last checkpoint.
	for _, content := range []string{
		"alpha old", "beta old", "gamma old", "delta old",
		"epsilon recent", "zeta recent", "eta recent", "theta recent",
		"iota recent", "kappa recent", "lambda recent", "mu recent",
	} {
		m, err = m.appendSessionEvent(sessions.EventMessage, map[string]any{
			"role":    "user",
			"content": content,
		})
		if err != nil {
			t.Fatal(err)
		}
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowUser, text: content})
	}
	m.input.SetValue("/compact status")

	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd != nil {
		t.Fatal("/compact status must not start an agent run")
	}
	text := ansi.Strip(transcriptText(next.transcript))
	for _, want := range []string{
		"Compact",
		"status: info",
		"context fill:",
		"% of", // % of the context window
		"compactable: yes — 4 turns older than the last checkpoint",
		"Result",
		"none this session",
		"hint: /compact now — summarize now, keep decisions and evidence verbatim",
		"compaction never drops settled decisions or receipts, only prose",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("compact status card missing %q:\n%s", want, text)
		}
	}
}

// P14 cell 12 (/loop list): the list card carries cadence, iteration cap, and
// the stop forms.
func TestLoopListCardCarriesCadenceCapAndStopForms(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m = startFixedLoop(m, "check the build", 5*time.Minute)
	m = startFixedLoop(m, "watch CI", 5*time.Minute)
	m.loops[0].maxIter = 60
	m.loops[0].iteration = 14
	m.loops[0].paused = true // keep the poll quiet for the assertion
	m.loops[1].paused = true

	m.input.SetValue("/loop list")
	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	text := ansi.Strip(transcriptText(next.transcript))
	for _, want := range []string{
		"Active loops:",
		"every 5m",
		"iter 14",
		"/loop stop all",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("loop list missing %q:\n%s", want, text)
		}
	}
}

// P14 cell 12: an out-of-band interval is clamped and the start ack SAYS SO.
func TestLoopStartSurfacesClampNotice(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.input.SetValue("/loop 5s check the build")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	text := ansi.Strip(transcriptText(next.transcript))
	for _, want := range []string{
		"started",
		"every 30s",
		"interval raised to the 30s minimum",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("loop clamp ack missing %q:\n%s", want, text)
		}
	}
	if len(next.loops) != 1 || next.loops[0].interval != loopMinInterval {
		t.Fatalf("expected one loop clamped to the minimum, got %#v", next.loops)
	}
}

// P14 cell 12 (negative): an in-band interval starts with no clamp notice.
func TestLoopStartInBandIntervalHasNoClampNotice(t *testing.T) {
	m := newModel(context.Background(), Options{})
	m.input.SetValue("/loop 5m check the build")

	updated, _ := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	text := ansi.Strip(transcriptText(next.transcript))
	if strings.Contains(text, "interval raised") || strings.Contains(text, "interval lowered") {
		t.Fatalf("in-band interval must not carry a clamp notice, got:\n%s", text)
	}
	if !strings.Contains(text, "Loop L1 started — every 5m") {
		t.Fatalf("expected a plain start ack, got:\n%s", text)
	}
}
