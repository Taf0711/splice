package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Taf0711/splice/internal/sessions"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
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

// A TUI worktree run must read through the raw source reader rooted in the
// worktree, never in the live checkout it was copied from.
func TestTUIWorktreeRegistryRebindsRawReaderToRunDir(t *testing.T) {
	live := t.TempDir()
	runDir := t.TempDir()
	base := tools.NewRegistry()
	tools.RegisterPipelineSourceReader(base, live, nil)
	if err := os.WriteFile(filepath.Join(runDir, "a.txt"), []byte("worktree bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(live, "b.txt"), []byte("live bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := tuiWorktreeRegistry(base, runDir)
	if _, ok := reg.Get(tools.RawFileReadToolName); !ok {
		t.Fatalf("worktree registry lacks %s", tools.RawFileReadToolName)
	}
	inRun := reg.RunWithOptions(context.Background(), tools.RawFileReadToolName, map[string]any{"path": "a.txt"}, tools.RunOptions{HostSeam: true})
	if inRun.Status != tools.StatusOK || inRun.Output != "worktree bytes" {
		t.Fatalf("worktree raw read = %s %q, want ok worktree bytes", inRun.Status, inRun.Output)
	}
	live2 := reg.RunWithOptions(context.Background(), tools.RawFileReadToolName, map[string]any{"path": filepath.Join(live, "b.txt")}, tools.RunOptions{HostSeam: true})
	if live2.Status == tools.StatusOK {
		t.Fatalf("worktree registry read the live checkout: %q", live2.Output)
	}
}
