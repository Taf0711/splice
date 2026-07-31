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
