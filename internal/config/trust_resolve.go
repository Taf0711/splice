package config

import (
	"errors"
	"io/fs"
	"os"
	"strings"
)

// ResolveTrust computes the effective trust decision for a workspace from CLI
// flags, environment, the persistent store, and the user's default setting.
//
// Precedence (highest first):
//  1. trustFlag true -> TrustTrusted, persist true
//  2. noTrustFlag true -> TrustDeclined, persist false
//  3. envValue == "1" -> TrustTrusted, no persist
//  4. envValue == "0" -> TrustDeclined, no persist
//  5. Saved store decision for the workspace -> that decision, no persist
//  6. setting == "always" -> TrustTrusted, no persist
//  7. setting == "never" -> TrustDeclined, no persist
//  8. setting == "ask" or empty -> TrustUndecided, no persist
func ResolveTrust(
	workspacePath string,
	store *TrustStore,
	setting string,
	trustFlag, noTrustFlag bool,
	envValue string,
) (decision TrustDecision, persist bool) {
	if trustFlag {
		return TrustTrusted, true
	}
	if noTrustFlag {
		return TrustDeclined, false
	}
	if envValue == "1" {
		return TrustTrusted, false
	}
	if envValue == "0" {
		return TrustDeclined, false
	}
	if store != nil {
		if d := store.IsTrusted(workspacePath); d != TrustUndecided {
			return d, false
		}
	}
	if setting == "always" {
		return TrustTrusted, false
	}
	if setting == "never" {
		return TrustDeclined, false
	}
	return TrustUndecided, false
}

// ProjectTrustSetting returns defaultProjectTrust from the user config at
// userConfigPath. It never reads a project config, so a project cannot set
// the value that decides its own trust. An empty path or a missing file gives
// "ask".
func ProjectTrustSetting(userConfigPath string) (string, error) {
	if userConfigPath == "" {
		return "ask", nil
	}
	userConfig, err := loadConfigFile(userConfigPath, "")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "ask", nil
		}
		return "ask", err
	}
	if trust := strings.TrimSpace(userConfig.DefaultProjectTrust); trust != "" {
		return trust, nil
	}
	return "ask", nil
}

// ResolveProjectTrust reports whether the project layer of workspaceRoot may
// load. It applies ResolveTrust to the CLI flags, SPLICE_TRUST_WORKSPACE, the
// trust store, and the user config setting (see ProjectTrustSetting). It does
// not persist a decision. On an error the result is false.
func ResolveProjectTrust(workspaceRoot, userConfigPath string, trustFlag, noTrustFlag bool) (bool, error) {
	setting, err := ProjectTrustSetting(userConfigPath)
	if err != nil {
		return false, err
	}
	storePath, err := DefaultTrustStorePath()
	if err != nil {
		return false, err
	}
	store, err := LoadTrustStore(storePath)
	if err != nil {
		return false, err
	}
	decision, _ := ResolveTrust(workspaceRoot, store, setting, trustFlag, noTrustFlag, os.Getenv("SPLICE_TRUST_WORKSPACE"))
	return decision == TrustTrusted, nil
}
