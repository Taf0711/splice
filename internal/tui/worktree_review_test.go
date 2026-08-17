package tui

import (
	"context"
	"testing"

	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

func TestVerdictForReview(t *testing.T) {
	cases := []struct {
		name     string
		decision string
		reason   string
		want     *schemas.VerdictRecord
	}{
		{"accept kept", worktreeReviewAccept, "", &schemas.VerdictRecord{Verdict: schemas.VerdictKept}},
		{"reject rejected with reason", worktreeReviewReject, worktreeRejectStillFailing, &schemas.VerdictRecord{Verdict: schemas.VerdictRejected, RejectReason: worktreeRejectStillFailing}},
		{"keep no record", worktreeReviewKeep, "", nil},
		{"empty no record", "", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := verdictForReview(tc.decision, tc.reason)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("verdict = %#v, want nil", got)
				}
				return
			}
			if got == nil || *got != *tc.want {
				t.Fatalf("verdict = %#v, want %#v", got, *tc.want)
			}
		})
	}
}

func TestTUIRejectPathWritesVerdictWithReason(t *testing.T) {
	var got schemas.VerdictRecord
	calls := 0
	orig := tuiUpsertVerdict
	tuiUpsertVerdict = func(_ context.Context, v schemas.VerdictRecord) error {
		calls++
		got = v
		return nil
	}
	defer func() { tuiUpsertVerdict = orig }()

	m := newModel(context.Background(), Options{})
	m.sessionStore = testSessionStore(t)
	sess, err := m.sessionStore.Create(sessions.CreateInput{SessionID: "sess-1", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m.activeSession = sess

	_, cmd := m.Update(worktreeReviewResultMsg{decision: worktreeReviewReject, reason: worktreeRejectStillFailing})
	if cmd == nil {
		t.Fatal("expected a verdict write command")
	}
	msg := execCmd(cmd)
	vmsg, ok := msg.(verdictWriteMsg)
	if !ok || vmsg.err != nil {
		t.Fatalf("verdict write msg = %#v", msg)
	}
	if calls != 1 {
		t.Fatalf("tuiUpsertVerdict calls = %d, want 1", calls)
	}
	if got.Verdict != schemas.VerdictRejected || got.RejectReason != worktreeRejectStillFailing {
		t.Fatalf("verdict = %#v, want rejected with reason %q", got, worktreeRejectStillFailing)
	}
	if got.RunID != "sess-1" {
		t.Fatalf("run id = %q, want sess-1", got.RunID)
	}
	if got.DecidedAt.IsZero() {
		t.Fatal("decided_at must be set")
	}
}

func TestTUIKeepPathWritesNoVerdict(t *testing.T) {
	calls := 0
	orig := tuiUpsertVerdict
	tuiUpsertVerdict = func(context.Context, schemas.VerdictRecord) error {
		calls++
		return nil
	}
	defer func() { tuiUpsertVerdict = orig }()

	m := newModel(context.Background(), Options{})
	m.sessionStore = testSessionStore(t)
	sess, err := m.sessionStore.Create(sessions.CreateInput{SessionID: "sess-1", Cwd: t.TempDir()})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	m.activeSession = sess

	_, cmd := m.Update(worktreeReviewResultMsg{decision: worktreeReviewKeep})
	if cmd != nil {
		msg := execCmd(cmd)
		t.Fatalf("keep must not write a verdict; got cmd -> %#v", msg)
	}
	if calls != 0 {
		t.Fatalf("tuiUpsertVerdict calls = %d, want 0", calls)
	}
}
