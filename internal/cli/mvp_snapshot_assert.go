package cli

// Snapshot assertion helpers for the matched-snapshots runner (B1): every
// Task B attempt re-verifies the intended commit AND tree AND a clean index
// and working tree, immediately before the model launches. A dirty file can
// coexist with the correct HEAD, so commit equality alone is insufficient;
// the assertions below treat commit and tree as separate facts.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// snapshotState is the asserted start state of one arm before a Task B run.
// Commit and tree are separate fields and separate facts: a commit hash is
// never relabeled as a tree hash.
type snapshotState struct {
	Commit string // git rev-parse HEAD
	Tree   string // git rev-parse HEAD^{tree}
}

// assertCleanSnapshot verifies, for one arm directory immediately before a
// Task B attempt, that:
//  1. HEAD is exactly the intended snapshot commit,
//  2. HEAD^{tree} is exactly the intended snapshot tree,
//  3. the index and working tree are clean, including unexpected untracked
//     source files (git status --porcelain with -z, robust to filenames with
//     spaces and quote-path handling).
//
// The returned snapshotState records what was actually verified so the
// assertion results can land on the row BEFORE the model launches. Every
// failure names the offending input and never silently passes.
func assertCleanSnapshot(dir, wantCommit, wantTree string) (snapshotState, error) {
	state := snapshotState{}
	if dir == "" {
		return state, fmt.Errorf("clean snapshot: no directory to assert")
	}
	if wantCommit == "" {
		return state, fmt.Errorf("clean snapshot: no intended commit for %s", dir)
	}
	if wantTree == "" {
		return state, fmt.Errorf("clean snapshot: no intended tree for %s", dir)
	}
	state.Commit = gitHeadCommit(dir)
	if state.Commit == "" {
		return state, fmt.Errorf("clean snapshot: no readable HEAD in %s", dir)
	}
	state.Tree = gitTreeHash(dir)
	if state.Tree == "" {
		return state, fmt.Errorf("clean snapshot: no readable tree in %s", dir)
	}
	if state.Commit != wantCommit {
		return state, fmt.Errorf("clean snapshot %s: HEAD commit %s does not match intended snapshot commit %s", dir, state.Commit, wantCommit)
	}
	if state.Tree != wantTree {
		return state, fmt.Errorf("clean snapshot %s: tree %s does not match intended snapshot tree %s", dir, state.Tree, wantTree)
	}
	// Porcelain -z separates entries with NUL, so filenames with spaces,
	// quotes, or newlines parse exactly. Any entry means the index or
	// working tree diverges from the frozen snapshot (tracked modifications,
	// stage entries, or untracked files the previous attempt left behind).
	entries, err := porcelainEntries(dir)
	if err != nil {
		return state, fmt.Errorf("clean snapshot %s: read git status: %w", dir, err)
	}
	if len(entries) > 0 {
		return state, fmt.Errorf("clean snapshot %s: working tree is not clean: %v", dir, entries)
	}
	return state, nil
}

// porcelainEntries runs `git status --porcelain -z --untracked-files=all`
// and returns the entry codes with their paths. -z never quotes paths, so
// no unquoting of core.quotePath output is needed; the parse splits on the
// NUL separator exactly.
func porcelainEntries(dir string) ([]string, error) {
	cmd := exec.Command("git", "-C", dir, "status", "--porcelain", "-z", "--untracked-files=all")
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var entries []string
	for _, record := range strings.Split(string(out), "\x00") {
		if strings.TrimSpace(record) == "" {
			continue
		}
		// Each record is "XY <path>" (two status letters, one space).
		// Rename records carry "XY <new> \x00 <old>" inside the same
		// record's embedded NUL, already handled by the outer split.
		if len(record) < 4 {
			entries = append(entries, record)
			continue
		}
		entries = append(entries, record[:2]+" "+record[3:])
	}
	return entries, nil
}
