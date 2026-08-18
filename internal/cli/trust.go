package cli

import (
	"os"

	"github.com/Taf0711/splice/internal/config"
)

// resolveWorkspaceTrust loads the persistent trust store and computes the
// effective trust decision for workspaceRoot from CLI flags, the
// SPLICE_TRUST_WORKSPACE environment variable, the saved store, and the
// supplied setting. It returns whether the workspace is trusted, the full
// decision, whether the caller should persist the decision, the loaded store,
// and any load error.
func resolveWorkspaceTrust(workspaceRoot string, setting string, trustFlag, noTrustFlag bool) (trusted bool, decision config.TrustDecision, persist bool, store *config.TrustStore, err error) {
	path, err := config.DefaultTrustStorePath()
	if err != nil {
		return false, config.TrustUndecided, false, nil, err
	}
	store, err = config.LoadTrustStore(path)
	if err != nil {
		return false, config.TrustUndecided, false, store, err
	}
	decision, persist = config.ResolveTrust(workspaceRoot, store, setting, trustFlag, noTrustFlag, os.Getenv("SPLICE_TRUST_WORKSPACE"))
	return decision == config.TrustTrusted, decision, persist, store, nil
}

// worktreeTrustInherit is the pure core of the trust-inheritance spike. When
// inherit is set and sameRepo proves the worktree belongs to its source repo,
// the source repo's recorded trust decision (no flags, no env) becomes the
// worktree's. It never widens trust beyond what the source repo already has:
// a declined or undecided source, a missing store, or a non-matching repo
// inherits nothing, so the fail-closed re-prompt path stays the default.
func worktreeTrustInherit(sourceRepoRoot string, store *config.TrustStore, setting string, inherit, sameRepo bool) (trusted bool, decision config.TrustDecision) {
	if !inherit || !sameRepo || store == nil {
		return false, config.TrustUndecided
	}
	decision, _ = config.ResolveTrust(sourceRepoRoot, store, setting, false, false, "")
	if decision == config.TrustTrusted {
		return true, decision
	}
	return false, config.TrustUndecided
}

// shouldPromptWorkspaceTrust reports whether the interactive first-run trust
// prompt is allowed to run. Keep this predicate separate so every security gate
// is explicit and easy to test.
func shouldPromptWorkspaceTrust(decision config.TrustDecision, setting string, trustFlag, noTrustFlag, envSet, stdinIsTerminal bool) bool {
	return decision == config.TrustUndecided &&
		(setting == "" || setting == "ask") &&
		!trustFlag && !noTrustFlag && !envSet && stdinIsTerminal
}

func envTrustWorkspaceSet() bool {
	_, ok := os.LookupEnv("SPLICE_TRUST_WORKSPACE")
	return ok
}
