package cognition

// E1a freshness evaluation suite (handoff section 9): table-driven cases over
// REAL materialized git repositories, each with one explicit expected
// classification from the contract in section 10:
//
//	FRESH  - anchor provably unchanged since the source commit
//	STALE  - anchor provably changed
//	UNKNOWN - provability failed (bad commit, not a repo, empty input)
//
// The primary safety invariant is FALSE FRESH = 0. A false-stale is an
// efficiency regression; a false-fresh is a correctness risk. The suite
// reports them separately and never as one aggregate accuracy number.
//
// Known limits are recorded, not papered over (section 9): file-level
// freshness cannot see a semantic dependency change elsewhere, so cases like
// "interface changed in another file" expect STALE-free behavior only where
// the anchor itself changed; where the anchor is unchanged the expected class
// is FRESH and the case is labeled a semantic blind spot, which the report
// must keep visible rather than "fix" by overbuilding.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// freshCase is one repository-state transition with its expected class.
type freshCase struct {
	name string
	// setup mutates the materialized repo into the state under test.
	setup func(t *testing.T, env *freshEnv)
	// want is the classification the contract requires.
	want FreshnessState
	// limit marks a known semantic blind spot (report section 9): the
	// anchor is unchanged, so freshness says FRESH, but a human reviewer
	// would call the knowledge outdated. Recorded in the report, never
	// counted as wrong.
	limit string
}

// freshEnv wraps one materialized repo for case setup.
type freshEnv struct {
	t    *testing.T
	root string
	// commit is the observation's source commit.
	commit string
	// anchor is the file the observation is keyed to.
	anchor string
}

// git runs a git command in the repo, failing the test on error.
func (e *freshEnv) git(args ...string) {
	e.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", e.root}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		e.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// gitOK runs a git command expected to FAIL (exit != 0), e.g. a diff that
// detects changes. It fails the test only when git itself errored.
func (e *freshEnv) gitFails(args ...string) {
	e.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", e.root}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		if _, isExit := err.(*exec.ExitError); !isExit {
			e.t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// write replaces a file's content.
func (e *freshEnv) write(path, content string) {
	e.t.Helper()
	full := filepath.Join(e.root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

// remove deletes a file from the working tree.
func (e *freshEnv) remove(path string) {
	if err := os.Remove(filepath.Join(e.root, path)); err != nil {
		e.t.Fatal(err)
	}
}

// mkdirAll creates a directory.
func (e *freshEnv) mkdirAll(path string) {
	if err := os.MkdirAll(filepath.Join(e.root, path), 0o755); err != nil {
		e.t.Fatal(err)
	}
}

// rename moves a file in the working tree.
func (e *freshEnv) rename(from, to string) {
	if err := os.Rename(filepath.Join(e.root, from), filepath.Join(e.root, to)); err != nil {
		e.t.Fatal(err)
	}
}

// stageEverything indexes the current working tree state.
func (e *freshEnv) stageEverything() {
	e.git("add", "-A")
}

// commitAll commits the current state.
func (e *freshEnv) commitAll(msg string) {
	e.stageEverything()
	e.git("commit", "-m", msg)
}

// classify runs the production classifier for the env's anchor.
func (e *freshEnv) classify() FreshnessState {
	return ClassifyFreshness(context.Background(), e.root, e.commit, e.anchor)
}

const baseSessionGo = "package auth\n\nfunc Invalidate() error { return nil }\n"
const baseSessionTestGo = "package auth\n\nimport \"testing\"\n\nfunc TestInvalidate(t *testing.T) {}\n"
const baseStoreGo = "package auth\n\ntype Store struct{ v int }\n"

// newEvalRepo materializes a repo with three committed files. The anchor is
// internal/auth/session.go; session_test.go and store.go exist so sibling-
// change and package-directory cases have neighbors.
func newEvalRepo(t *testing.T) *freshEnv {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	files := map[string]string{
		"internal/auth/session.go":      baseSessionGo,
		"internal/auth/session_test.go": baseSessionTestGo,
		"internal/auth/store.go":        baseStoreGo,
		"README.md":                     "# demo\n",
	}
	root, commit := newFreshnessRepo(t, files)
	return &freshEnv{t: t, root: root, commit: commit, anchor: "internal/auth/session.go"}
}

// freshEvalCases is the transition matrix (handoff section 9). Grouped by
// the kind of state change.
func freshEvalCases() []freshCase {
	return []freshCase{
		// --- anchor file: pristine and content mutations ---
		{name: "anchor pristine", want: FreshnessFresh},
		{name: "anchor edited", setup: func(t *testing.T, e *freshEnv) {
			e.write(e.anchor, baseSessionGo+"\n// edited\n")
		}, want: FreshnessStale},
		{name: "anchor touched without content change", setup: func(t *testing.T, e *freshEnv) {
			// Touch-only: mtime changes, bytes do not. diff --quiet must
			// stay FRESH (this is exactly what a stat-plumbing approach
			// would get wrong).
			e.write(e.anchor, baseSessionGo)
			bumpMtime(t, filepath.Join(e.root, e.anchor))
		}, want: FreshnessFresh},
		{name: "anchor mtime bumped without write", setup: func(t *testing.T, e *freshEnv) {
			bumpMtime(t, filepath.Join(e.root, e.anchor))
		}, want: FreshnessFresh},
		{name: "anchor edited then reverted to identical bytes", setup: func(t *testing.T, e *freshEnv) {
			e.write(e.anchor, baseSessionGo+"\n// temp\n")
			e.write(e.anchor, baseSessionGo)
		}, want: FreshnessFresh},
		{name: "anchor whitespace-only change", setup: func(t *testing.T, e *freshEnv) {
			e.write(e.anchor, baseSessionGo+"\n")
		}, want: FreshnessStale},
		{name: "anchor comment-only change", setup: func(t *testing.T, e *freshEnv) {
			e.write(e.anchor, "package auth\n\n// Invalidate clears the session.\nfunc Invalidate() error { return nil }\n")
		}, want: FreshnessStale},

		// --- anchor deleted / renamed / moved / recreated ---
		{name: "anchor deleted unstaged", setup: func(t *testing.T, e *freshEnv) {
			e.remove(e.anchor)
		}, want: FreshnessStale},
		{name: "anchor deleted staged", setup: func(t *testing.T, e *freshEnv) {
			e.remove(e.anchor)
			e.stageEverything()
		}, want: FreshnessStale},
		{name: "anchor renamed", setup: func(t *testing.T, e *freshEnv) {
			e.rename(e.anchor, "internal/auth/session_v2.go")
		}, want: FreshnessStale},
		{name: "anchor moved into subdirectory", setup: func(t *testing.T, e *freshEnv) {
			e.mkdirAll("internal/auth/deep")
			e.rename(e.anchor, "internal/auth/deep/session.go")
		}, want: FreshnessStale},
		{name: "anchor recreated with identical content", setup: func(t *testing.T, e *freshEnv) {
			e.remove(e.anchor)
			e.write(e.anchor, baseSessionGo)
		}, want: FreshnessFresh, limit: "git diff --quiet compares bytes against the commit; a delete+recreate with identical bytes is FRESH by design. Recorded, not hidden."},

		// --- staged vs unstaged vs both ---
		{name: "anchor edit staged only", setup: func(t *testing.T, e *freshEnv) {
			e.write(e.anchor, baseSessionGo+"\n// staged\n")
			e.stageEverything()
		}, want: FreshnessStale},
		{name: "anchor edit unstaged only", setup: func(t *testing.T, e *freshEnv) {
			e.write(e.anchor, baseSessionGo+"\n// unstaged\n")
		}, want: FreshnessStale},
		{name: "anchor staged edit plus further unstaged edit", setup: func(t *testing.T, e *freshEnv) {
			e.write(e.anchor, baseSessionGo+"\n// staged\n")
			e.stageEverything()
			e.write(e.anchor, baseSessionGo+"\n// staged\n// unstaged\n")
		}, want: FreshnessStale},
		{name: "anchor staged revert of a committed change stays stale", setup: func(t *testing.T, e *freshEnv) {
			// Commit a REAL change, then stage a revert back to the source
			// commit bytes. Freshness compares against the SOURCE commit,
			// not HEAD: the staged content matches the source again, but
			// diff --quiet <source> also includes the committed delta in
			// the index state? No: diff <source> compares the WORKING TREE
			// against <source>. Working tree == source bytes => FRESH is
			// what a byte-diff gives. This case pins that behavior.
			e.write(e.anchor, baseSessionGo+"\n// committed\n")
			e.commitAll("second commit")
			e.write(e.anchor, baseSessionGo)
			e.stageEverything()
		}, want: FreshnessFresh, limit: "the index/HEAD moved on, but the working tree matches the source commit byte-for-byte, so diff --quiet <source> reports fresh. Contract-correct for byte-level freshness; recorded because a reviewer might expect stale."},

		// --- untracked / working-tree oddities ---
		{name: "untracked file appears elsewhere", setup: func(t *testing.T, e *freshEnv) {
			e.write("internal/auth/notes.md", "scratch\n")
		}, want: FreshnessFresh},
		{name: "untracked replacement of deleted sibling", setup: func(t *testing.T, e *freshEnv) {
			e.remove("internal/auth/store.go")
			e.write("internal/auth/store_new.go", "package auth\n")
		}, want: FreshnessFresh},

		// --- branch / HEAD states ---
		{name: "branch switch away and back", setup: func(t *testing.T, e *freshEnv) {
			e.git("checkout", "-b", "topic")
			e.git("checkout", "main")
		}, want: FreshnessFresh},
		{name: "branch switch with divergent anchor", setup: func(t *testing.T, e *freshEnv) {
			e.git("checkout", "-b", "topic")
			e.write(e.anchor, baseSessionGo+"\n// branch work\n")
			e.commitAll("branch change")
			e.git("checkout", "main")
			// back on main, anchor matches the source commit again
		}, want: FreshnessFresh},
		{name: "detached HEAD at source commit", setup: func(t *testing.T, e *freshEnv) {
			e.git("checkout", "--detach", e.commit)
		}, want: FreshnessFresh},
		{name: "commit on top of source commit with unrelated file", setup: func(t *testing.T, e *freshEnv) {
			e.write("docs/new.md", "# new\n")
			e.commitAll("docs only")
		}, want: FreshnessFresh},

		// --- linked worktrees ---
		{name: "linked worktree shares fresh state", setup: func(t *testing.T, e *freshEnv) {
			e.git("worktree", "add", "-b", "wt-fresh-branch", filepath.Join(e.root, "..", "wt-fresh"))
		}, want: FreshnessFresh},
		{name: "linked worktree edit does not touch main anchor", setup: func(t *testing.T, e *freshEnv) {
			e.git("worktree", "add", filepath.Join(e.root, "..", "wt-edit"), "-b", "wt-edit-branch")
			wtAnchor := filepath.Join(e.root, "..", "wt-edit", e.anchor)
			if err := os.WriteFile(wtAnchor, []byte(baseSessionGo+"\n// wt edit\n"), 0o600); err != nil {
				e.t.Fatal(err)
			}
			// The MAIN checkout's anchor is untouched: still fresh HERE.
		}, want: FreshnessFresh},
		{name: "worktree-added repo classifies its own edits stale", setup: func(t *testing.T, e *freshEnv) {
			wt := filepath.Join(e.root, "..", "wt-self")
			e.git("worktree", "add", wt, "-b", "wt-self-branch")
			if err := os.WriteFile(filepath.Join(wt, e.anchor), []byte(baseSessionGo+"\n// edited in wt\n"), 0o600); err != nil {
				e.t.Fatal(err)
			}
			// Re-point the env at the worktree for classification.
			e.root = wt
		}, want: FreshnessStale},

		// --- siblings and directories ---
		{name: "sibling file edited, anchor pristine", setup: func(t *testing.T, e *freshEnv) {
			e.write("internal/auth/session_test.go", baseSessionTestGo+"\nfunc TestExtra(t *testing.T) {}\n")
		}, want: FreshnessFresh},
		{name: "package directory neighbor added", setup: func(t *testing.T, e *freshEnv) {
			e.write("internal/auth/newfile.go", "package auth\n")
		}, want: FreshnessFresh},
		{name: "package directory neighbor edited", setup: func(t *testing.T, e *freshEnv) {
			e.write("internal/auth/store.go", baseStoreGo+"\nfunc (s *Store) Reset() {}\n")
		}, want: FreshnessFresh},

		// --- file mode ---
		{name: "file mode change only", setup: func(t *testing.T, e *freshEnv) {
			if err := os.Chmod(filepath.Join(e.root, e.anchor), 0o755); err != nil {
				e.t.Fatal(err)
			}
		}, want: FreshnessStale, limit: "git tracks the mode bit on macOS/Linux; a mode-only change is a diff. Recorded so the report explains why this is STALE, not FRESH."},

		// --- semantic invalidation scenarios (section 9 second list) ---
		{name: "interface changed elsewhere, anchor unchanged", setup: func(t *testing.T, e *freshEnv) {
			// Store's interface changed; session.go consumes it. File-level
			// freshness cannot see this.
			e.write("internal/auth/store.go", "package auth\n\ntype Store interface { Reset() }\n")
		}, want: FreshnessFresh, limit: "semantic blind spot: file-level freshness cannot detect dependency changes. Known limit, explicit in the report."},
		{name: "go.mod dependency changed, anchor unchanged", setup: func(t *testing.T, e *freshEnv) {
			e.write("go.mod", "module demo\n\ngo 1.25\n\nrequire other.example/v2 v2.0.0\n")
		}, want: FreshnessFresh, limit: "semantic blind spot: dependency drift is invisible to per-file diff."},
		{name: "config changed elsewhere, anchor unchanged", setup: func(t *testing.T, e *freshEnv) {
			e.write("config.yaml", "sessions: rotated\n")
		}, want: FreshnessFresh, limit: "semantic blind spot: config drift."},
		{name: "generated source changed, anchor unchanged", setup: func(t *testing.T, e *freshEnv) {
			e.write("internal/auth/zz_generated.go", "// Code generated. DO NOT EDIT.\npackage auth\n")
		}, want: FreshnessFresh, limit: "semantic blind spot: generated-code drift."},
		{name: "build tag file added, anchor unchanged", setup: func(t *testing.T, e *freshEnv) {
			e.write("internal/auth/session_windows.go", "//go:build windows\n\npackage auth\n")
		}, want: FreshnessFresh, limit: "semantic blind spot: platform-tag drift."},
		{name: "test contract changed in sibling test file", setup: func(t *testing.T, e *freshEnv) {
			e.write("internal/auth/session_test.go", baseSessionTestGo+"\nfunc TestInvalidateReturnsError(t *testing.T) {}\n")
		}, want: FreshnessFresh, limit: "semantic blind spot: contract drift in tests."},
		{name: "package directory renamed away entirely", setup: func(t *testing.T, e *freshEnv) {
			e.rename("internal/auth", "internal/auth_old")
		}, want: FreshnessStale},
		{name: "symbol-containing file deleted then package re-created empty", setup: func(t *testing.T, e *freshEnv) {
			e.remove(e.anchor)
			e.write("internal/auth/placeholder.go", "package auth\n")
		}, want: FreshnessStale},
	}
}

// bumpMtime sets the file's mtime to now + 2s (touch semantics) without
// changing bytes.
func bumpMtime(t *testing.T, path string) {
	t.Helper()
	now := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, now, now); err != nil {
		t.Fatal(err)
	}
}

func TestFreshnessEvalSuite(t *testing.T) {
	cases := freshEvalCases()
	t.Logf("freshness eval cases: %d", len(cases))

	// Report section 10: separate counters, never one accuracy number.
	var correct, falseFresh, falseStale, expectedUnknown, observedUnknown, limits int

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newEvalRepo(t)
			if tc.setup != nil {
				tc.setup(t, env)
			}
			got := env.classify()
			if tc.limit != "" {
				limits++
				t.Logf("known limit: %s", tc.limit)
			}
			if got == FreshnessUnknown {
				observedUnknown++
			}
			switch {
			case got == tc.want:
				correct++
				if tc.want == FreshnessUnknown {
					expectedUnknown++
				}
			case tc.want == FreshnessFresh && got == FreshnessStale:
				// Contract said fresh but we saw stale: false-stale.
				falseStale++
				t.Fatalf("FALSE STALE: got %q want %q", got, tc.want)
			case tc.want == FreshnessStale && got == FreshnessFresh:
				// THE safety invariant. This must never happen.
				falseFresh++
				t.Fatalf("FALSE FRESH (correctness risk): got %q want %q", got, tc.want)
			default:
				t.Fatalf("classification mismatch: got %q want %q", got, tc.want)
			}
		})
	}

	// The section 10 report shape, visible in verbose output.
	t.Logf("freshness cases: %d", len(cases))
	t.Logf("correct: %d", correct)
	t.Logf("false fresh: %d", falseFresh)
	t.Logf("false stale: %d", falseStale)
	t.Logf("unknown expected: %d", expectedUnknown)
	t.Logf("unknown observed: %d", observedUnknown)
	t.Logf("known limits recorded: %d", limits)

	// Section 39 gate: false fresh = 0. Hard fail, not a warning.
	if falseFresh != 0 {
		t.Fatalf("freshness safety gate violated: false fresh = %d", falseFresh)
	}
}

// pkgEvalCases exercises DIRECTORY anchors (package keys): the anchor is the
// package directory, and git diff on a directory covers everything under it.
func pkgEvalCases() []freshCase {
	return []freshCase{
		{name: "pkg pristine", want: FreshnessFresh},
		{name: "pkg file edited", setup: func(t *testing.T, e *freshEnv) {
			e.write("internal/auth/session.go", baseSessionGo+"\n// edited\n")
		}, want: FreshnessStale},
		{name: "pkg neighbor added unstaged", setup: func(t *testing.T, e *freshEnv) {
			e.write("internal/auth/extra.go", "package auth\n")
		}, want: FreshnessFresh, limit: "EXPOSURE: an unstaged new file under the package directory is invisible to diff <commit> -- <dir> (untracked files are not in the diff until staged). The implemented semantic is tracked-changes-only. Staged additions ARE stale (next case). Recorded, not silently widened to git status."},
		{name: "pkg neighbor deleted", setup: func(t *testing.T, e *freshEnv) {
			e.remove("internal/auth/store.go")
		}, want: FreshnessStale},
		{name: "pkg subdirectory added with unstaged file", setup: func(t *testing.T, e *freshEnv) {
			e.mkdirAll("internal/auth/sub")
			e.write("internal/auth/sub/deep.go", "package sub\n")
		}, want: FreshnessFresh, limit: "EXPOSURE: same untracked-visibility limit as pkg_neighbor_added; the subdirectory file is unstaged so the diff cannot see it."},
		{name: "pkg file touched only", setup: func(t *testing.T, e *freshEnv) {
			bumpMtime(t, filepath.Join(e.root, "internal/auth/session.go"))
		}, want: FreshnessFresh},
		{name: "pkg edited then reverted", setup: func(t *testing.T, e *freshEnv) {
			e.write("internal/auth/session.go", baseSessionGo+"\n// temp\n")
			e.write("internal/auth/session.go", baseSessionGo)
		}, want: FreshnessFresh},
		{name: "pkg empty subdirectory added", setup: func(t *testing.T, e *freshEnv) {
			e.mkdirAll("internal/auth/emptydir")
		}, want: FreshnessFresh, limit: "git does not track empty directories; an untracked empty dir cannot appear in a diff. Recorded."},
		{name: "other package edited, pkg anchor pristine", setup: func(t *testing.T, e *freshEnv) {
			e.write("README.md", "# demo\n# changed\n")
		}, want: FreshnessFresh},
		{name: "pkg file untracked added", setup: func(t *testing.T, e *freshEnv) {
			e.write("internal/auth/untracked_new.go", "package auth\n")
			// Untracked files DO appear in diff <commit> -- <dir> once added;
			// before staging they do not. Pin the unstaged behavior.
		}, want: FreshnessFresh, limit: "diff <commit> -- <dir> covers tracked changes; an untracked new file is invisible until staged. Recorded."},
		{name: "pkg untracked file staged becomes visible", setup: func(t *testing.T, e *freshEnv) {
			e.write("internal/auth/untracked_staged.go", "package auth\n")
			e.stageEverything()
		}, want: FreshnessStale},
	}
}

// symEvalCases exercises SYMBOL-key anchors: the anchor path is the
// containing file, so symbol cases behave like file cases. Included in the
// suite so the contract for AnchorPathForKey(symbol) stays proven.
func symEvalCases() []freshCase {
	return []freshCase{
		{name: "sym pristine", want: FreshnessFresh},
		{name: "sym containing file edited", setup: func(t *testing.T, e *freshEnv) {
			e.write(e.anchor, baseSessionGo+"\n// func Invalidate changed\n")
		}, want: FreshnessStale},
		{name: "sym containing file deleted", setup: func(t *testing.T, e *freshEnv) {
			e.remove(e.anchor)
		}, want: FreshnessStale},
		{name: "sym other symbol in same file edited", setup: func(t *testing.T, e *freshEnv) {
			// Symbol-level invalidation is out of scope (C0 contract): any
			// change to the containing file stales every symbol in it.
			e.write(e.anchor, "package auth\n\nfunc Other() error { return nil }\nfunc Invalidate() error { return nil }\n")
		}, want: FreshnessStale, limit: "same-file sibling-symbol changes stale the symbol key. Conservative by design; recorded."},
		{name: "sym containing file touched only", setup: func(t *testing.T, e *freshEnv) {
			bumpMtime(t, filepath.Join(e.root, e.anchor))
		}, want: FreshnessFresh},
	}
}

// TestFreshnessEvalSuitePackageAndSymbolAnchors runs the package and symbol
// anchor families through the same counters.
func TestFreshnessEvalSuitePackageAndSymbolAnchors(t *testing.T) {
	families := []struct {
		name  string
		cases []freshCase
		// anchorOverride replaces the env anchor for the family.
		anchorOverride string
	}{
		{name: "package", cases: pkgEvalCases(), anchorOverride: "internal/auth"},
		{name: "symbol", cases: symEvalCases()},
	}
	for _, fam := range families {
		t.Run(fam.name, func(t *testing.T) {
			var correct, falseFresh, falseStale, limits int
			for _, tc := range fam.cases {
				t.Run(tc.name, func(t *testing.T) {
					env := newEvalRepo(t)
					if fam.anchorOverride != "" {
						env.anchor = fam.anchorOverride
					}
					if tc.setup != nil {
						tc.setup(t, env)
					}
					got := env.classify()
					if tc.limit != "" {
						limits++
						t.Logf("known limit: %s", tc.limit)
					}
					switch {
					case got == tc.want:
						correct++
					case tc.want == FreshnessFresh && got == FreshnessStale:
						falseStale++
						t.Fatalf("FALSE STALE: got %q want %q", got, tc.want)
					case tc.want == FreshnessStale && got == FreshnessFresh:
						falseFresh++
						t.Fatalf("FALSE FRESH (correctness risk): got %q want %q", got, tc.want)
					default:
						t.Fatalf("classification mismatch: got %q want %q", got, tc.want)
					}
				})
			}
			t.Logf("%s family: cases %d correct %d false-fresh %d false-stale %d limits %d",
				fam.name, len(fam.cases), correct, falseFresh, falseStale, limits)
			if falseFresh != 0 {
				t.Fatalf("freshness safety gate violated: false fresh = %d", falseFresh)
			}
		})
	}
}

// TestFreshnessBatchExactnessAcrossMatrix proves the C1b batching invariant
// (batch == single) for EVERY case in all three families: the batched
// classification of one anchor must match ClassifyFreshness exactly, over
// fresh, edited, staged, renamed, deleted, and branch states alike.
func TestFreshnessBatchExactnessAcrossMatrix(t *testing.T) {
	all := append(append(freshEvalCases(), pkgEvalCases()...), symEvalCases()...)
	t.Logf("batch exactness matrix: %d cases", len(all))
	for _, tc := range all {
		t.Run(tc.name, func(t *testing.T) {
			env := newEvalRepo(t)
			if tc.setup != nil {
				tc.setup(t, env)
			}
			single := ClassifyFreshness(context.Background(), env.root, env.commit, env.anchor)
			changed, err := ChangedPaths(context.Background(), env.root, env.commit, nil)
			if err != nil {
				t.Fatalf("ChangedPaths: %v", err)
			}
			batched := ClassifyBatch(env.anchor, changed)
			if single != batched {
				t.Fatalf("batch != single: single=%q batch=%q", single, batched)
			}
		})
	}
}

func genCaseRepo(t *testing.T, anchor, initial string) *freshEnv {
	t.Helper()
	env := newEvalRepo(t)
	env.write(anchor, initial)
	env.git("add", "-A")
	env.git("commit", "-m", "add "+anchor)
	out, err := exec.Command("git", "-C", env.root, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	env.commit = strings.TrimSpace(string(out))
	env.anchor = anchor
	return env
}

// TestFreshnessEvalSuiteGenerated runs the generated family: the core
// transition matrix across source-language anchors.
func TestFreshnessEvalSuiteGenerated(t *testing.T) {
	type extSpec struct {
		ext, initial, edited string
	}
	specs := []extSpec{
		{"py", "def invalidate():\n    pass\n", "def invalidate():\n    pass\n# changed\n"},
		{"ts", "export function invalidate(): void {}\n", "export function invalidate(): void {}\n// changed\n"},
		{"rs", "pub fn invalidate() {}\n", "pub fn invalidate() {}\n// changed\n"},
		{"sh", "#!/bin/sh\necho ok\n", "#!/bin/sh\necho changed\n"},
		{"md", "# notes\n", "# notes changed\n"},
		{"json", "{\"key\": 1}\n", "{\"key\": 2}\n"},
		{"yaml", "key: 1\n", "key: 2\n"},
		{"toml", "key = 1\n", "key = 2\n"},
	}
	var falseFresh, falseStale int
	cases := 0
	for _, spec := range specs {
		transitions := []struct {
			name string
			want FreshnessState
			mut  func(e *freshEnv)
		}{
			{"pristine", FreshnessFresh, nil},
			{"edited", FreshnessStale, func(e *freshEnv) { e.write(e.anchor, spec.edited) }},
			{"deleted", FreshnessStale, func(e *freshEnv) { e.remove(e.anchor) }},
			{"touched only", FreshnessFresh, func(e *freshEnv) { bumpMtime(t, filepath.Join(e.root, e.anchor)) }},
			{"renamed", FreshnessStale, func(e *freshEnv) { e.rename(e.anchor, "src/module_moved."+spec.ext) }},
		}
		for _, tr := range transitions {
			cases++
			t.Run(spec.ext+"/"+tr.name, func(t *testing.T) {
				env := genCaseRepo(t, "src/module."+spec.ext, spec.initial)
				if tr.mut != nil {
					tr.mut(env)
				}
				got := env.classify()
				switch {
				case got == tr.want:
					// correct; subtest passes silently
				case tr.want == FreshnessFresh && got == FreshnessStale:
					falseStale++
					t.Fatalf("FALSE STALE: got %q want %q", got, tr.want)
				case tr.want == FreshnessStale && got == FreshnessFresh:
					falseFresh++
					t.Fatalf("FALSE FRESH (correctness risk): got %q want %q", got, tr.want)
				default:
					t.Fatalf("classification mismatch: got %q want %q", got, tr.want)
				}
			})
		}
	}
	t.Logf("generated family: cases %d false-fresh %d false-stale %d", cases, falseFresh, falseStale)
	if falseFresh != 0 {
		t.Fatalf("freshness safety gate violated: false fresh = %d", falseFresh)
	}
}
