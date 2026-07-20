package config

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
