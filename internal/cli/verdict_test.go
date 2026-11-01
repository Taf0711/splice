package cli

import (
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/worktrees"
)

func TestVerdictForMergeStatus(t *testing.T) {
	cases := []struct {
		name   string
		status worktrees.MergeBackStatus
		sha    string
		branch string
		want   *schemas.VerdictRecord
	}{
		{
			name:   "merged kept with sha and branch",
			status: worktrees.MergeBackMerged,
			sha:    "abc123",
			branch: "splice/task-a",
			want:   &schemas.VerdictRecord{Verdict: schemas.VerdictKept, MergeCommitSHA: "abc123", MergeBranch: "splice/task-a"},
		},
		{name: "no changes no record", status: worktrees.MergeBackNoChanges, want: nil},
		{name: "conflict no record", status: worktrees.MergeBackConflict, want: nil},
		{name: "skipped dirty no record", status: worktrees.MergeBackSkippedDirty, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := verdictForMergeStatus(tc.status, tc.sha, tc.branch)
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
