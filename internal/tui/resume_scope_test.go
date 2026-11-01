package tui

import (
	"runtime"
	"testing"

	"github.com/Taf0711/splice/internal/sessions"
)

func TestSessionMatchesWorkspace(t *testing.T) {
	cases := []struct {
		name        string
		sessionCwd  string
		workspace   string
		wantVisible bool
	}{
		{"same workspace", "/home/u/proj", "/home/u/proj", true},
		{"trailing slash normalizes", "/home/u/proj/", "/home/u/proj", true},
		{"different workspace hidden", "/home/u/other", "/home/u/proj", false},
		{"session with no cwd stays visible", "", "/home/u/proj", true},
		{"unknown current workspace keeps all", "/home/u/other", "", true},
		// Casing: matched case-insensitively on Windows (its FS is), case-sensitively
		// elsewhere. This exercises the platform-specific branch on each OS's CI.
		{"case differs only", "/home/U/Proj", "/home/u/proj", runtime.GOOS == "windows"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionMatchesWorkspace(tc.sessionCwd, tc.workspace); got != tc.wantVisible {
				t.Fatalf("sessionMatchesWorkspace(%q, %q) = %v, want %v", tc.sessionCwd, tc.workspace, got, tc.wantVisible)
			}
		})
	}
}

// TestSessionWorkspaceMatchOrigin covers the TW4 OR rule: a worktree session
// matches its workspace through either its execution Cwd or its OriginCwd (the
// source repo it was launched from), so it stays visible from both.
func TestSessionWorkspaceMatchOrigin(t *testing.T) {
	cases := []struct {
		name        string
		cwd         string
		originCwd   string
		workspace   string
		wantVisible bool
	}{
		{"worktree matches source repo via origin", "/home/u/proj/.wt/1", "/home/u/proj", "/home/u/proj", true},
		{"worktree matches its own checkout", "/home/u/proj/.wt/1", "/home/u/proj", "/home/u/proj/.wt/1", true},
		{"worktree hidden from unrelated workspace", "/home/u/proj/.wt/1", "/home/u/proj", "/home/u/other", false},
		{"plain session matches its cwd, ignores empty origin", "/home/u/proj", "", "/home/u/proj", true},
		{"plain session hidden from other workspace", "/home/u/other", "", "/home/u/proj", false},
		{"no cwd and no origin stays visible", "", "", "/home/u/proj", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			meta := sessions.Metadata{Cwd: tc.cwd, OriginCwd: tc.originCwd}
			if got := sessionWorkspaceMatch(meta, tc.workspace); got != tc.wantVisible {
				t.Fatalf("sessionWorkspaceMatch({Cwd:%q, OriginCwd:%q}, %q) = %v, want %v", tc.cwd, tc.originCwd, tc.workspace, got, tc.wantVisible)
			}
		})
	}
}

func TestIsWorktreeSession(t *testing.T) {
	if !isWorktreeSession(sessions.Metadata{Cwd: "/repo/.wt/1", OriginCwd: "/repo"}) {
		t.Fatal("origin differing from cwd should mark a worktree session")
	}
	if isWorktreeSession(sessions.Metadata{Cwd: "/repo", OriginCwd: ""}) {
		t.Fatal("empty origin should not mark a worktree session")
	}
	if isWorktreeSession(sessions.Metadata{Cwd: "/repo", OriginCwd: "/repo"}) {
		t.Fatal("origin equal to cwd should not mark a worktree session")
	}
}
