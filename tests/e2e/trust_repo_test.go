package e2e

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hostileConfigLimit bounds each command on a hostile repo that only reads
// the config (spec H-04): `config` and `doctor`. A hostile config file that
// makes the read hang or spin goes past it.
const hostileConfigLimit = 30 * time.Second

// hostileExecLimit bounds `exec` on a hostile repo. Exec also runs a full
// pipeline (sandboxed git, go build, and go test), which can take more than
// 30 s on a slow or loaded host, so it gets 90 s. A hang still goes past it.
const hostileExecLimit = 90 * time.Second

// canary returns a random string that no honest output contains.
func canary(t *testing.T) string {
	t.Helper()
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return "CANARY-" + hex.EncodeToString(b)
}

// TestH04InRepoSymlinkControl is the H-04 control: a trusted run reads an
// in-repo AGENTS.md symlink, so the canary reaches a provider request. This
// proves the canary check can see a leak.
func TestH04InRepoSymlinkControl(t *testing.T) {
	t.Parallel()
	s := NewScenario(t)
	c := canary(t)
	writeFile(t, filepath.Join(s.Workspace, "rules", "AGENTS.md"), "Project rules. "+c+"\n")
	writeSymlink(t, filepath.Join("rules", "AGENTS.md"), filepath.Join(s.Workspace, "AGENTS.md"))
	s.Provider.Script(StageCodeWriter, SubmitCode(h01Prompt, File("main.go", "modify", helloMain)))
	s.Run("--trust", "exec", "-o", "stream-json", h01Prompt).WantExit(t, 0)
	if !requestBodiesContain(s, c) {
		t.Errorf("trusted run did not send the in-repo AGENTS.md; the canary check cannot see a leak")
	}
}

// requestBodiesContain reports whether any recorded provider request body has s.
func requestBodiesContain(s *Scenario, needle string) bool {
	for _, req := range s.Provider.Requests() {
		if strings.Contains(string(req.Body), needle) {
			return true
		}
	}
	return false
}

// timed runs fn and fails the test when it takes longer than limit.
func timed(t *testing.T, name string, limit time.Duration, fn func()) {
	t.Helper()
	start := time.Now()
	fn()
	if d := time.Since(start); d > limit {
		t.Errorf("%s took %s, limit %s", name, d, limit)
	}
}

// TestH04HostileProjectFiles is spec H-04 in an untrusted workspace. Each
// hostile file must not stop, hang, or leak the commands, and the canary in
// the outside AGENTS.md never reaches a provider request.
func TestH04HostileProjectFiles(t *testing.T) {
	t.Parallel()
	cases := map[string]func(t *testing.T, s *Scenario, canaryText string){
		"devzero-config": func(t *testing.T, s *Scenario, _ string) {
			writeSymlink(t, "/dev/zero", filepath.Join(s.Workspace, ".splice", "config.json"))
		},
		"two-mib-config": func(t *testing.T, s *Scenario, _ string) {
			writeFile(t, filepath.Join(s.Workspace, ".splice", "config.json"), strings.Repeat(" ", 2<<20)+"{}\n")
		},
		"outside-agents-symlink": func(t *testing.T, s *Scenario, canaryText string) {
			outside := filepath.Join(s.Root, "outside", "AGENTS.md")
			writeFile(t, outside, "Project rules. "+canaryText+"\n")
			writeSymlink(t, outside, filepath.Join(s.Workspace, "AGENTS.md"))
		},
	}
	for name, plant := range cases {
		plant := plant
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s := NewScenario(t)
			c := canary(t)
			plant(t, s, c)

			timed(t, "config", hostileConfigLimit, func() { s.Run("config") })
			timed(t, "doctor", hostileConfigLimit, func() { s.Run("doctor") })
			s.Provider.Script(StageCodeWriter, SubmitCode(h01Prompt, File("main.go", "modify", helloMain)))
			var r Result
			timed(t, "exec", hostileExecLimit, func() { r = s.Run("exec", "-o", "stream-json", h01Prompt) })
			r.WantExit(t, 0)

			if requestBodiesContain(s, c) {
				t.Errorf("canary from the hostile file reached a provider request")
			}
		})
	}
}

// writeSymlink makes link point at target.
func writeSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// h05Marker names the fsmonitor marker of one scenario.
func h05Marker(s *Scenario, name string) string {
	return "sh -c 'touch " + s.Marker(name) + "'"
}

// TestH05FsmonitorControlPlainRepo is the H-05 fixture control: the harness's
// own unhardened git status runs core.fsmonitor from the fixture's config.
func TestH05FsmonitorControlPlainRepo(t *testing.T) {
	t.Parallel()
	s := NewScenario(t)
	s.Git("config", "core.fsmonitor", h05Marker(s, "fsm-control"))
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = s.Workspace
	cmd.Env = s.ChildEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	if !s.MarkerExists("fsm-control") {
		t.Fatalf("unhardened git status did not run core.fsmonitor; the control is broken")
	}
}

// TestH05FsmonitorPlainRepo is spec H-05 in a plain repo: exec and
// `splice changes` must not run the repo's core.fsmonitor.
func TestH05FsmonitorPlainRepo(t *testing.T) {
	t.Parallel()
	s := NewScenario(t)
	s.Git("config", "core.fsmonitor", h05Marker(s, "fsm-plain"))

	r := s.Run("changes")
	if s.MarkerExists("fsm-plain") {
		t.Errorf("splice changes ran the repo core.fsmonitor (exit %d)", r.ExitCode)
	}

	s.Provider.Script(StageCodeWriter, SubmitCode(h01Prompt, File("main.go", "modify", helloMain)))
	r = s.Run("exec", "-o", "stream-json", h01Prompt)
	if s.MarkerExists("fsm-plain") {
		t.Errorf("splice exec ran the repo core.fsmonitor from an untrusted repo")
	}
	// The run must reach its git calls: an exec that dies early runs no git
	// and leaves no marker, so the check above alone cannot fail.
	r.WantExit(t, 0)
}

// TestH05FsmonitorEmbeddedBareRepo is spec H-05 for an embedded bare repo
// with core.worktree=.., launched from that subfolder. The bare layout sits at
// sub/.git (core.bare=false, core.worktree=..) because a bare repo at
// sub/embedded.git gives the pipeline no git call, so it proves nothing.
func TestH05FsmonitorEmbeddedBareRepo(t *testing.T) {
	t.Parallel()
	s := NewScenario(t)
	sub := filepath.Join(s.Workspace, "sub")
	gitDir := filepath.Join(sub, ".git")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = s.ChildEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "--bare", gitDir)
	git("--git-dir", gitDir, "config", "core.bare", "false")
	git("--git-dir", gitDir, "config", "core.worktree", "..")
	git("--git-dir", gitDir, "config", "core.fsmonitor", h05Marker(s, "fsm-embedded"))

	r := s.RunIn(sub, "changes")
	if s.MarkerExists("fsm-embedded") {
		t.Errorf("splice changes from the embedded repo ran its core.fsmonitor (exit %d)", r.ExitCode)
	}

	s.Provider.Script(StageCodeWriter, SubmitCode(h01Prompt, File("main.go", "modify", helloMain)))
	r = s.RunIn(sub, "exec", "-o", "stream-json", h01Prompt)
	if s.MarkerExists("fsm-embedded") {
		t.Errorf("splice exec from the embedded repo ran its core.fsmonitor from an untrusted repo")
	}
	// The run must reach its git calls: an exec that dies early runs no git
	// and leaves no marker, so the check above alone cannot fail.
	r.WantExit(t, 0)
}
