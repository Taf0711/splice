package cli

// E2 treatment execution path pins (reviewer findings R1 and R2 from the
// c572088 push review). Both defects were silent: the run still completed,
// the attempt row still recorded a treatment, and only the realized child
// disagreed with what the row claimed. These tests assert the ACTUAL argv
// and the ACTUAL child environment, not the source text that builds them.

import (
	"os"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/eval"
	"github.com/Taf0711/splice/internal/splice"
)

// argvValue returns the value following flag in args, and whether the flag
// was present exactly once. Reading the argv by NAME is the point: the bug
// under test was positional index arithmetic that wrote a value over a
// neighbouring flag.
func argvValue(args []string, flag string) (string, bool) {
	seen := 0
	value := ""
	for i, arg := range args {
		if arg == flag {
			seen++
			if i+1 < len(args) {
				value = args[i+1]
			}
		}
	}
	return value, seen == 1
}

// R1: an explicit treatment must set the --memory VALUE and must not
// disturb any other flag. The pre-fix code wrote spec.PromptMemory at
// index 6, which is the --init-session-id FLAG, so every explicit-treatment
// child was launched with argv ...--memory <in.Memory> "on" <sessionID>...
// The session id flag was destroyed and the run's trace could not be joined.
func TestPairEvalArgvTreatmentSetsMemoryValueNotSessionFlag(t *testing.T) {
	in := eval.RunInput{
		SessionID: "sess-abc-123",
		Memory:    "off",
		Prompt:    "do the task",
	}
	for _, name := range []string{"cold", "retrieval-only", "delivery-only", "scope-only", "full"} {
		t.Run(name, func(t *testing.T) {
			spec, err := splice.ResolveTreatment(name)
			if err != nil {
				t.Fatal(err)
			}
			args := pairEvalArgv(in, "", spec, true)

			// The --init-session-id flag must survive intact and carry
			// the requested session id.
			got, ok := argvValue(args, "--init-session-id")
			if !ok {
				t.Fatalf("--init-session-id missing or duplicated in argv: %q", args)
			}
			if got != in.SessionID {
				t.Fatalf("session id = %q, want %q (argv: %q)", got, in.SessionID, args)
			}
			// The memory flag carries the TREATMENT's value: the
			// treatment is the authority when set.
			mem, ok := argvValue(args, "--memory")
			if !ok {
				t.Fatalf("--memory missing or duplicated in argv: %q", args)
			}
			if mem != spec.PromptMemory {
				t.Fatalf("memory = %q, want the treatment's %q (argv: %q)", mem, spec.PromptMemory, args)
			}
			// No treatment value may leak into a flag position.
			for _, arg := range args {
				if arg == "on" || arg == "off" {
					if prior, _ := argvValue(args, "--memory"); prior != arg {
						t.Fatalf("stray memory literal %q in argv: %q", arg, args)
					}
				}
			}
		})
	}
}

// Without a treatment the input's own memory mode is used verbatim.
func TestPairEvalArgvWithoutTreatmentUsesInputMemory(t *testing.T) {
	in := eval.RunInput{SessionID: "sess-1", Memory: "on", Prompt: "p"}
	args := pairEvalArgv(in, "", splice.TreatmentSpec{}, false)
	mem, ok := argvValue(args, "--memory")
	if !ok || mem != "on" {
		t.Fatalf("memory = %q (ok=%v), want on (argv: %q)", mem, ok, args)
	}
	sess, ok := argvValue(args, "--init-session-id")
	if !ok || sess != "sess-1" {
		t.Fatalf("session id = %q (ok=%v), want sess-1 (argv: %q)", sess, ok, args)
	}
}

// The prompt is always last, so a prompt beginning with a dash cannot be
// parsed as a flag by the child, and --model appears only when requested.
func TestPairEvalArgvPromptLastAndModelOptional(t *testing.T) {
	in := eval.RunInput{SessionID: "s", Memory: "on", Prompt: "the prompt"}

	bare := pairEvalArgv(in, "", splice.TreatmentSpec{}, false)
	if bare[len(bare)-1] != "the prompt" {
		t.Fatalf("prompt must be the final argument, got %q", bare)
	}
	if _, present := argvValue(bare, "--model"); present {
		t.Fatalf("--model must be absent when no model is requested: %q", bare)
	}

	withModel := pairEvalArgv(in, "claude-opus-4.1", splice.TreatmentSpec{}, false)
	if withModel[len(withModel)-1] != "the prompt" {
		t.Fatalf("prompt must remain final with a model set, got %q", withModel)
	}
	model, ok := argvValue(withModel, "--model")
	if !ok || model != "claude-opus-4.1" {
		t.Fatalf("model = %q (ok=%v), want claude-opus-4.1 (argv: %q)", model, ok, withModel)
	}
}

// envValue returns the final value of key in env. Final wins, because that
// is how a process resolves duplicate entries.
func envValue(env []string, key string) (string, bool) {
	value := ""
	found := false
	for _, entry := range env {
		if strings.HasPrefix(entry, key+"=") {
			value = strings.TrimPrefix(entry, key+"=")
			found = true
		}
	}
	return value, found
}

// R2: an ambient SPLICE_TREATMENT must not override the explicitly
// requested treatment. resolveExemplarMode and resolveScopeMode give
// SPLICE_TREATMENT precedence, so appending the spec's entries to a raw
// os.Environ() left the INHERITED treatment in charge: a child requested as
// "full" under ambient SPLICE_TREATMENT=cold ran with delivery and scope
// disabled while the attempt row recorded "full".
func TestPairEvalChildEnvExplicitTreatmentBeatsAmbient(t *testing.T) {
	ambient := []string{
		"PATH=/usr/bin",
		splice.TreatmentEnvVar + "=cold",
		splice.ExemplarModeEnvVar + "=none",
		splice.ScopeModeEnvVar + "=off",
		"HOME=/home/tester",
	}
	spec, err := splice.ResolveTreatment("full")
	if err != nil {
		t.Fatal(err)
	}
	env := pairEvalChildEnv(ambient, spec)

	// All three treatment-owned variables carry the REQUESTED treatment.
	if _, present := envValue(env, splice.TreatmentEnvVar); present {
		t.Fatal("ambient SPLICE_TREATMENT survived into the child env and would override the requested treatment")
	}
	exemplar, ok := envValue(env, splice.ExemplarModeEnvVar)
	if !ok || exemplar != string(spec.ExemplarMode) {
		t.Fatalf("exemplar mode = %q (ok=%v), want %q", exemplar, ok, spec.ExemplarMode)
	}
	scope, ok := envValue(env, splice.ScopeModeEnvVar)
	if !ok || scope != "on" {
		t.Fatalf("scope mode = %q (ok=%v), want on", scope, ok)
	}
	// Unrelated environment is preserved: the child still needs PATH.
	if path, ok := envValue(env, "PATH"); !ok || path != "/usr/bin" {
		t.Fatalf("PATH = %q (ok=%v), want /usr/bin", path, ok)
	}
	if home, ok := envValue(env, "HOME"); !ok || home != "/home/tester" {
		t.Fatalf("HOME = %q (ok=%v), want /home/tester", home, ok)
	}
}

// Every treatment must survive ambient contradiction, not just full: the
// ambient value is whatever the operator's shell happened to export.
func TestPairEvalChildEnvRealizesEveryRequestedTreatment(t *testing.T) {
	for _, requested := range []string{"cold", "retrieval-only", "delivery-only", "scope-only", "full"} {
		for _, ambientTreatment := range []string{"cold", "full", "scope-only"} {
			t.Run(requested+"_under_"+ambientTreatment, func(t *testing.T) {
				spec, err := splice.ResolveTreatment(requested)
				if err != nil {
					t.Fatal(err)
				}
				ambient := []string{
					splice.TreatmentEnvVar + "=" + ambientTreatment,
					splice.ExemplarModeEnvVar + "=obs-only",
					splice.ScopeModeEnvVar + "=off",
				}
				env := pairEvalChildEnv(ambient, spec)

				if _, present := envValue(env, splice.TreatmentEnvVar); present {
					t.Fatal("SPLICE_TREATMENT must not reach the child; it outranks the knobs the spec sets")
				}
				exemplar, _ := envValue(env, splice.ExemplarModeEnvVar)
				if exemplar != string(spec.ExemplarMode) {
					t.Fatalf("exemplar mode = %q, want the requested treatment's %q", exemplar, spec.ExemplarMode)
				}
				wantScope := "off"
				if spec.ScopeOnlyContext {
					wantScope = "on"
				}
				if scope, _ := envValue(env, splice.ScopeModeEnvVar); scope != wantScope {
					t.Fatalf("scope mode = %q, want %q", scope, wantScope)
				}
			})
		}
	}
}

// A realized-treatment probe: apply the constructed child environment to
// this process, then run the production resolvers against it. This closes
// the loop that the env-construction test alone cannot: it proves the
// resolvers actually report the requested treatment's dimensions.
func TestPairEvalChildEnvRealizedByResolvers(t *testing.T) {
	spec, err := splice.ResolveTreatment("full")
	if err != nil {
		t.Fatal(err)
	}
	ambient := []string{
		splice.TreatmentEnvVar + "=cold",
		splice.ExemplarModeEnvVar + "=none",
		splice.ScopeModeEnvVar + "=off",
	}
	env := pairEvalChildEnv(ambient, spec)

	// Apply the constructed env to this process for the duration of the
	// test. t.Setenv restores the prior values on cleanup.
	t.Setenv(splice.TreatmentEnvVar, "cold")
	os.Unsetenv(splice.TreatmentEnvVar)
	if raw, present := envValue(env, splice.TreatmentEnvVar); present {
		t.Setenv(splice.TreatmentEnvVar, raw)
	}
	exemplar, _ := envValue(env, splice.ExemplarModeEnvVar)
	t.Setenv(splice.ExemplarModeEnvVar, exemplar)
	scope, _ := envValue(env, splice.ScopeModeEnvVar)
	t.Setenv(splice.ScopeModeEnvVar, scope)

	realized, err := splice.RealizedTreatmentDimensions()
	if err != nil {
		t.Fatalf("resolvers rejected the constructed env: %v", err)
	}
	if !realized.ScopeOn {
		t.Error("realized scope = off, want on for full")
	}
	if realized.ExemplarMode != splice.ExemplarModeBoth {
		t.Errorf("realized exemplar mode = %q, want both for full", realized.ExemplarMode)
	}
	if !realized.PromptDelivery {
		t.Error("realized prompt delivery = false, want true for full")
	}
}
