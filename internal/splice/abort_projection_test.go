package splice

// Regression probes for the typed abort kind (review finding 2): CANCELLED
// means the USER chose to stop; internal aborts project FAILED. These tests
// fail on the pre-fix code, which keyed the projection on a non-empty
// abort reason (every internal abort sets one).

import (
	"testing"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/presentation"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

func probePresentationFor(t *testing.T, result schemas.PipelineResult) presentation.State {
	t.Helper()
	var got presentation.State
	options := PipelineRunConfig{
		OnPresentationState: func(s presentation.State) { got = s },
		OnStageEvent:        func(event agent.StageEvent) {},
	}
	options, acc := wirePresentation(options, schemas.ExecutionPlan{RequestIntent: "probe intent"})
	if acc == nil {
		t.Fatal("probe setup: accumulator nil")
	}
	acc.Apply(presentrun.AdaptPlanEvent("probe intent", []string{"code_writer"}))
	options.OnPresentationState(acc.Snapshot())
	finishPresentation(acc, options, result)
	return got
}

// Internal aborts must project FAILED with the reason preserved. The
// pre-fix code projected CANCELLED for every one of these.
func TestFinishPresentationInternalAbortsProjectFailed(t *testing.T) {
	cases := []struct {
		name   string
		reason string
	}{
		{"max iterations", "reached max iterations (3) without success"},
		{"wall time", "wall time exceeded"},
		{"rollback refuse", "rollback requires an isolated worktree: score regression"},
		{"surface abort no callback", "surface_to_user: needs input (no interactive callback; aborting)"},
		{"decision abort", "rollback: score below floor"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := probePresentationFor(t, schemas.PipelineResult{
				RunID:       "probe",
				Status:      "aborted",
				AbortReason: &tc.reason,
			})
			if got.Health == presentation.HealthCancelled {
				t.Fatalf("probe: internal abort %q projected CANCELLED; cancelled means the user chose to stop", tc.reason)
			}
			if got.Completion == nil || got.Completion.Status != "failed" {
				t.Fatalf("probe: internal abort %q completion = %+v, want failed", tc.reason, got.Completion)
			}
			if got.Completion != nil && got.Completion.Detail != tc.reason {
				t.Fatalf("probe: reason not preserved: got %q want %q", got.Completion.Detail, tc.reason)
			}
		})
	}
}

// The typed user abort must keep projecting CANCELLED with staged
// semantics.
func TestFinishPresentationUserAbortedProjectsCancelled(t *testing.T) {
	reason := "user aborted: stop requested"
	got := probePresentationFor(t, schemas.PipelineResult{
		RunID:       "probe",
		Status:      "aborted",
		AbortReason: &reason,
		UserAborted: true,
	})
	if got.Health != presentation.HealthCancelled {
		t.Fatalf("probe: user abort projected health %q, want cancelled", got.Health)
	}
	if got.Completion == nil || got.Completion.Status != "cancelled" {
		t.Fatalf("probe: user abort completion = %+v, want cancelled", got.Completion)
	}
}

// Producer/consumer pairing: finishWithUserAbort is the ONLY constructor
// that sets UserAborted, and finishWithReason NEVER sets it. If a new
// internal abort constructor starts setting UserAborted, or the user-abort
// site stops setting it, this fails.
func TestUserAbortKindPairing(t *testing.T) {
	plan := schemas.ExecutionPlan{RequestIntent: "pair", Tier: schemas.TierLight}
	internal, err := finishWithReason("r", plan, nil, "aborted", "wall time exceeded")
	if err != nil {
		t.Fatal(err)
	}
	if internal.UserAborted {
		t.Fatal("probe: finishWithReason set UserAborted; internal aborts must stay typed as non-user")
	}
	user, err := finishWithUserAbort("r", plan, nil, "user aborted: stop requested")
	if err != nil {
		t.Fatal(err)
	}
	if !user.UserAborted || user.Status != "aborted" || user.AbortReason == nil {
		t.Fatalf("probe: finishWithUserAbort produced %+v, want aborted+UserAborted+reason", user)
	}
}
