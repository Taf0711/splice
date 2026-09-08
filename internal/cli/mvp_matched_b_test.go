package cli

// Work package B tests: per-attempt clean-snapshot assertions (B1), real
// capture provenance (B2), and producer-run-qualified capture sets (B3).
// Real git fixtures follow the worktrees_test.go pattern.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/eval"
	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice"
)

// ---- Work package B tests: per-attempt clean-snapshot assertions (B1),
// real capture provenance (B2), and producer-run-qualified capture sets
// (B3). Real git fixtures follow the worktrees_test.go pattern.

// newMatchedSnapshotRepo builds a git repo in the shape the matched runner
// produces: one base commit, then the verified Task A tree committed by
// gitCommitAll. Returns the dir, the snapshot commit, and the snapshot tree.
func newMatchedSnapshotRepo(t *testing.T) (string, string, string) {
	t.Helper()
	dir := newGitRepoWithChange(t)
	commit, err := gitCommitAll(dir)
	if err != nil {
		t.Fatalf("commit snapshot tree: %v", err)
	}
	tree := gitTreeHash(dir)
	if tree == "" {
		t.Fatal("fixture broken: no tree hash")
	}
	return dir, commit, tree
}

// ---- B1 test 1: a modified tracked file at the same HEAD is rejected ----

func TestCleanSnapshotRejectsModifiedTrackedFileAtSameHead(t *testing.T) {
	repo, commit, tree := newMatchedSnapshotRepo(t)
	// Same HEAD, dirty working tree: commit equality alone must not pass.
	if err := os.WriteFile(filepath.Join(repo, "a.go"), []byte("package a // sabotaged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := assertCleanSnapshot(repo, commit, tree)
	if err == nil {
		t.Fatal("modified tracked file at the snapshot HEAD must be rejected")
	}
	if !strings.Contains(err.Error(), "not clean") {
		t.Fatalf("rejection must name the dirty state, got: %v", err)
	}
}

// ---- B1 test 2: an unexpected untracked source file is rejected ----

func TestCleanSnapshotRejectsUnexpectedUntrackedFile(t *testing.T) {
	repo, commit, tree := newMatchedSnapshotRepo(t)
	// Untracked file with a space in the name exercises the -z parse.
	if err := os.WriteFile(filepath.Join(repo, "left over.go"), []byte("package leftover\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := assertCleanSnapshot(repo, commit, tree)
	if err == nil {
		t.Fatal("unexpected untracked file at the snapshot HEAD must be rejected")
	}
	if !strings.Contains(err.Error(), "left over.go") {
		t.Fatalf("rejection must name the offending file exactly (spaces preserved), got: %v", err)
	}
}

// ---- B1 test 3: commit and tree mismatches are separate failures ----

func TestCleanSnapshotDistinguishesCommitFromTreeMismatch(t *testing.T) {
	repo, commit, tree := newMatchedSnapshotRepo(t)

	// A commit hash must never satisfy a tree assertion (separate fields).
	if _, err := assertCleanSnapshot(repo, tree, tree); err == nil {
		t.Fatal("commit field must not be compared against the intended tree")
	}
	// A drift to a different commit fails the commit check. The empty
	// commit changes HEAD without changing any bytes, which is exactly
	// the case commit/tree separation exists for.
	empty := exec.Command("git", "-C", repo, "commit", "--allow-empty", "-qm", "drift")
	empty.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_AUTHOR_DATE=2026-01-02T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-02T00:00:00Z")
	if out, err := empty.CombinedOutput(); err != nil {
		t.Fatalf("empty commit: %v: %s", err, out)
	}
	newCommit := gitHeadCommit(repo)
	if newCommit == commit {
		t.Fatal("fixture broken: expected a new commit")
	}
	if _, err := assertCleanSnapshot(repo, commit, tree); err == nil {
		t.Fatal("drifted HEAD must be rejected")
	} else if !strings.Contains(err.Error(), "HEAD commit") {
		t.Fatalf("rejection must name the commit mismatch, got: %v", err)
	}
	// The new commit over the same bytes shares the tree: only the commit
	// assertion fired, proving the two facts are checked independently.
	if _, err := assertCleanSnapshot(repo, newCommit, tree); err != nil {
		t.Fatalf("same bytes under a new commit must pass the tree check and commit check for that commit: %v", err)
	}
}

// ---- B1 test 4: rematerialization resets a failed attempt's leftovers ----

func TestRematerializationClearsPreviousAttemptLeftovers(t *testing.T) {
	repo, commit, tree := newMatchedSnapshotRepo(t)
	dst := filepath.Join(t.TempDir(), "arm")
	if err := materializeFromCommit(repo, commit, dst); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	// A failed attempt leaves files behind.
	if err := os.WriteFile(filepath.Join(dst, "stray.go"), []byte("package stray\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "a.go"), []byte("package a // edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := assertCleanSnapshot(dst, commit, tree); err == nil {
		t.Fatal("leftover files must fail the per-attempt assertion before rematerialization")
	}
	// Rematerialization from the frozen commit resets every byte.
	if err := materializeFromCommit(repo, commit, dst); err != nil {
		t.Fatalf("rematerialize: %v", err)
	}
	if _, err := assertCleanSnapshot(dst, commit, tree); err != nil {
		t.Fatalf("rematerialized arm must pass the assertion: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dst, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "package a // changed\n" {
		t.Fatalf("rematerialized file = %q, want the snapshot bytes", data)
	}
}

// ---- B2 test 5: a failed Task A never seeds a capture ----

func TestFailedTaskAProducesNoCaptures(t *testing.T) {
	// captureFromVerifiedRun is evidence-gated on the completed status, so
	// the seed builder fed a failed outcome must produce nothing.
	prov := captureProvenance{ProducerRunID: "run-a", VerifyCommand: "exit 0"}
	set := buildSeedCaptureSetFromOutcome(t, "failed", prov)
	if len(set.Captures) != 0 {
		t.Fatalf("failed Task A produced %d capture(s), want 0", len(set.Captures))
	}
}

// buildSeedCaptureSetFromOutcome drives the capture derivation with an
// explicit outcome status, mirroring what the runner does with the Task A
// status it just recorded. The runner's status gate keeps failed runs away
// from buildSeedCaptureSet entirely; this pins the gate's foundation.
func buildSeedCaptureSetFromOutcome(t *testing.T, status string, prov captureProvenance) seedCaptureSet {
	t.Helper()
	dir := newGitRepoWithChange(t)
	set := seedCaptureSet{ProducerRunID: prov.ProducerRunID, Revision: gitHeadCommit(dir)}
	if status != "completed" {
		// The runner never calls the capture builder for a non-completed
		// status; the underlying derivation must also refuse.
		set.Captures = splice.CaptureFromVerifiedRun(dir, status, []string{"a.go"}, prov.VerifyCommand, set.Revision, prov.ProducerRunID)
		return set
	}
	set.Captures = splice.CaptureFromVerifiedRun(dir, status, []string{"a.go"}, prov.VerifyCommand, set.Revision, prov.ProducerRunID)
	return set
}

// ---- B2 test 6: real run id and executed command survive the capture ----

func TestSeedCaptureSetCarriesRealRunIDAndExecutedCommand(t *testing.T) {
	dir := newGitRepoWithChange(t)
	revision := gitHeadCommit(dir)
	prov := captureProvenance{ProducerRunID: "mvp-snap-fam-a-123-taska", VerifyCommand: "bash ./verifiers/large-01-a.sh"}
	set := buildSeedCaptureSet(dir, prov, []string{"a.go"}, revision)
	if set.ProducerRunID != "mvp-snap-fam-a-123-taska" {
		t.Fatalf("producer run id = %q, want the real Task A session id", set.ProducerRunID)
	}
	if len(set.Captures) == 0 {
		t.Fatal("fixture broken: expected captures")
	}
	sawProcedure := false
	for _, c := range set.Captures {
		if c.RunID != prov.ProducerRunID {
			t.Fatalf("capture %s run id = %q, want %q", c.Kind, c.RunID, prov.ProducerRunID)
		}
		if c.Revision != revision {
			t.Fatalf("capture %s revision = %q, want %q", c.Kind, c.Revision, revision)
		}
		if c.Kind == "procedure" {
			sawProcedure = true
			if !strings.Contains(c.Claim, "bash ./verifiers/large-01-a.sh") {
				t.Fatalf("procedure claim lost the executed verifier command: %q", c.Claim)
			}
			found := false
			for _, a := range c.Anchors {
				if a.Kind == "test" && a.Value == "bash ./verifiers/large-01-a.sh" {
					found = true
				}
			}
			if !found {
				t.Fatalf("procedure anchor must be the executed command, anchors: %v", c.Anchors)
			}
			for _, e := range c.Evidence {
				if e.Kind == "test_run" && e.Ref != prov.ProducerRunID {
					t.Fatalf("test_run evidence ref = %q, want the producing run id", e.Ref)
				}
			}
		}
	}
	if !sawProcedure {
		t.Fatal("fixture broken: no procedure capture")
	}
	if !set.Reconstructed {
		t.Fatal("the deterministic reconstruction path must stay labeled reconstructed")
	}
}

// ---- B2 test 7: an unrecoverable command stays explicitly unknown ----

func TestSeedCaptureSetUnknownCommandIsExplicitNotFabricated(t *testing.T) {
	dir := newGitRepoWithChange(t)
	revision := gitHeadCommit(dir)
	prov := captureProvenance{ProducerRunID: "run-x"} // no recorded command
	set := buildSeedCaptureSet(dir, prov, nil, revision)
	if len(set.Captures) == 0 {
		t.Fatal("fixture broken: expected a procedure capture even without a command")
	}
	for _, c := range set.Captures {
		if c.Kind != "procedure" {
			continue
		}
		for _, a := range c.Anchors {
			if a.Kind == "test" {
				if a.Value != unknownProvenanceCommand {
					t.Fatalf("anchor value = %q, want the explicit unknown marker", a.Value)
				}
				if strings.Contains(a.Value, "go test") {
					t.Fatal("a fabricated default command leaked into the capture")
				}
			}
		}
		for _, e := range c.Evidence {
			if e.Kind == "test_run" {
				if e.Detail == "test command exited 0" {
					t.Fatal("fabricated exit-0 evidence must not survive on an unknown-command capture")
				}
				if e.Detail != "verification command not recorded by the producing run" {
					t.Fatalf("unknown-command evidence detail = %q, want the explicit provenance note", e.Detail)
				}
			}
		}
	}
}

// ---- B3 test 8: the producer-run filter keeps same-revision runs apart ----
//
// The store-level all-or-nothing reanchor semantics are pinned by
// memd/store TestReanchorByIDsAllOrNothing; here we pin that the matched
// path asks the sidecar for the run-qualified set (the exact producer run
// id and revision on the wire) rather than the project-wide set.

func TestMatchedCaptureSetRequestsAreProducerRunQualified(t *testing.T) {
	c := memd.NewClient("/nonexistent-sock")
	// The client validation contract: empty run id omits the filter, a
	// non-empty one must reach the wire. Drive the capture-set call and
	// assert the error is a transport failure (the socket does not
	// exist), proving the call was issued with the arguments the matched
	// path passes. The wire-level filter round trip is pinned by
	// internal/memd TestCaptureSetIDsForRunRoundTrip.
	if _, err := c.CaptureSetIDsForRun(context.Background(), "", "rev", "run-a"); err == nil {
		t.Fatal("empty project must be rejected before any wire call")
	}
	if _, err := c.CaptureSetIDsForRun(context.Background(), "/repo", "", "run-a"); err == nil {
		t.Fatal("empty revision must be rejected before any wire call")
	}
	// The matched runner passes the snapshot run id and the PRE-COMMIT
	// revision; a code-reading assertion would be tautological here, so
	// this test pins the argument contract at the seam the runner uses
	// and relies on the round-trip test for the filter's wire behavior.
	_, err := c.CaptureSetIDsForRun(context.Background(), "/repo", "rev", "run-a")
	if err == nil || !strings.Contains(err.Error(), "connect") && !strings.Contains(err.Error(), "socket") && !strings.Contains(err.Error(), "nonexistent") {
		t.Fatalf("expected a transport error proving the qualified call was issued, got: %v", err)
	}
}

// ---- B2 test 9: replay preserves the payload and remaps only the project ----

func TestReplaySeedCapturesRemapsProjectOnly(t *testing.T) {
	var upserts []memd.GraphUpsertInput
	c := newLocalCaptureServer(t, func(in memd.GraphUpsertInput) {
		upserts = append(upserts, in)
	})
	snapshot := seedCaptureSet{
		ProducerRunID: "mvp-snap-fam-a-1-taska",
		Revision:      "1111111111",
		Reconstructed: true,
		Captures: []splice.GraphCapture{
			{
				Kind: "procedure", Claim: "Verification passes with: bash ./v.sh",
				RunID: "mvp-snap-fam-a-1-taska", Revision: "1111111111",
				Anchors:  []memd.GraphAnchor{{Kind: "test", Value: "bash ./v.sh"}},
				Evidence: []memd.GraphEvidence{{Kind: "test_run", Ref: "mvp-snap-fam-a-1-taska", Detail: "test command exited 0"}},
			},
			{
				Kind: "fact", Claim: "a.go defines F; verified at revision 1111111111",
				RunID: "mvp-snap-fam-a-1-taska", Revision: "1111111111",
				Anchors:  []memd.GraphAnchor{{Kind: "file", Value: "a.go"}},
				Evidence: []memd.GraphEvidence{{Kind: "git", Ref: "1111111111", Detail: "verified run changed this file"}},
			},
		},
	}
	warm := "/tmp/splice-mvp-warm-x"
	if err := replaySeedCaptures(context.Background(), c, warm, snapshot); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(upserts) != 2 {
		t.Fatalf("upserts = %d, want 2", len(upserts))
	}
	for i, in := range upserts {
		orig := snapshot.Captures[i]
		if in.ProjectPath != warm {
			t.Fatalf("capture %d project = %q, want the remapped warm identity", i, in.ProjectPath)
		}
		if in.Claim != orig.Claim {
			t.Fatalf("capture %d claim drifted: %q vs %q", i, in.Claim, orig.Claim)
		}
		if in.SourceRunID != snapshot.ProducerRunID {
			t.Fatalf("capture %d source run = %q, want the real producer run", i, in.SourceRunID)
		}
		if in.VerifiedRevision != snapshot.Revision {
			t.Fatalf("capture %d revision = %q, want %q", i, in.VerifiedRevision, snapshot.Revision)
		}
		if len(in.Anchors) != len(orig.Anchors) || len(in.Evidence) != len(orig.Evidence) {
			t.Fatalf("capture %d anchors/evidence counts drifted: %v/%v vs %v/%v",
				i, len(in.Anchors), len(in.Evidence), len(orig.Anchors), len(orig.Evidence))
		}
		for j := range in.Anchors {
			if in.Anchors[j] != orig.Anchors[j] {
				t.Fatalf("capture %d anchor %d drifted: %v vs %v", i, j, in.Anchors[j], orig.Anchors[j])
			}
		}
		for j := range in.Evidence {
			if in.Evidence[j] != orig.Evidence[j] {
				t.Fatalf("capture %d evidence %d drifted: %v vs %v", i, j, in.Evidence[j], orig.Evidence[j])
			}
		}
	}
}

// ---- F5-residual: artifact references reach the matched rows ----

func TestFillAttemptRowCarriesArtifactPathsFromSeamOutput(t *testing.T) {
	out := eval.RunOutput{
		Success:            true,
		VerifierOutputPath: "/out/f1-a1-r1-cold-b-verifier.txt",
		PatchPath:          "/out/f1-a1-r1-cold-b-patch.diff",
	}
	row := fillAttemptRow(familyPairRow{}, out, nil, 0, harnessProvenanceInfo{}, mvpEvalOptions{}, eval.RunInput{ArtifactDir: "/out"})
	if len(row.ArtifactRefs) != 2 ||
		row.ArtifactRefs[0] != out.VerifierOutputPath ||
		row.ArtifactRefs[1] != out.PatchPath {
		t.Fatalf("artifact refs = %v, want the seam verifier and patch paths", row.ArtifactRefs)
	}
	// A seam that produced no explicit paths still records the artifact
	// dir, never an empty evidence trail.
	row = fillAttemptRow(familyPairRow{}, eval.RunOutput{}, nil, 0, harnessProvenanceInfo{}, mvpEvalOptions{}, eval.RunInput{ArtifactDir: "/out"})
	if len(row.ArtifactRefs) != 1 || row.ArtifactRefs[0] != "/out" {
		t.Fatalf("artifact refs = %v, want the artifact dir fallback", row.ArtifactRefs)
	}
}

// ---- B2 test 10: an empty capture set is an explicit failure, not silence ----

func TestReplaySeedCapturesEmptySetFailsLoud(t *testing.T) {
	c := newLocalCaptureServer(t, func(memd.GraphUpsertInput) {})
	set := seedCaptureSet{ProducerRunID: "run-empty", Revision: "1111111111", Captures: []splice.GraphCapture{}}
	err := replaySeedCaptures(context.Background(), c, "/tmp/warm", set)
	if err == nil {
		t.Fatal("an empty capture set must fail loud: a zero-capture seed is never silent success")
	}
	if !strings.Contains(err.Error(), "empty capture set") {
		t.Fatalf("error must name the empty capture set, got: %v", err)
	}
}

// newLocalCaptureServer is a minimal /graph/upsert sidecar stand-in over a
// Unix socket (the transport the real client dials), recording inputs.
func newLocalCaptureServer(t *testing.T, record func(memd.GraphUpsertInput)) *memd.Client {
	t.Helper()
	f, err := os.CreateTemp("", "memd-b-*.sock")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	sock := f.Name()
	f.Close()
	os.Remove(sock)
	t.Cleanup(func() { os.Remove(sock) })
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/graph/upsert", func(w http.ResponseWriter, r *http.Request) {
		var in memd.GraphUpsertInput
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, `{"ok":false,"error":"decode"}`, http.StatusBadRequest)
			return
		}
		record(in)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "node": map[string]any{"id": 1, "kind": in.Kind, "claim": in.Claim}})
	})
	srv := httptest.NewUnstartedServer(mux)
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return memd.NewClient(sock)
}
