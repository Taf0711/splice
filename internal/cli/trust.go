package cli

import (
	"os"

	"github.com/Taf0711/splice/internal/config"
)

// resolveWorkspaceTrust loads the persistent trust store and computes the
// effective trust decision for workspaceRoot from CLI flags, the
// SPLICE_TRUST_WORKSPACE environment variable, the saved store, and the
// supplied setting. It returns whether the workspace is trusted, whether the
// caller should persist the decision, the loaded store, and any load error.
func resolveWorkspaceTrust(workspaceRoot string, setting string, trustFlag, noTrustFlag bool) (trusted bool, persist bool, store *config.TrustStore, err error) {
	path, err := config.DefaultTrustStorePath()
	if err != nil {
		return false, false, nil, err
	}
	store, err = config.LoadTrustStore(path)
	if err != nil {
		return false, false, store, err
	}
	decision, persist := config.ResolveTrust(workspaceRoot, store, setting, trustFlag, noTrustFlag, os.Getenv("SPLICE_TRUST_WORKSPACE"))
	return decision == config.TrustTrusted, persist, store, nil
}
