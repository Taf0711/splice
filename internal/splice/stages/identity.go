package stages

import "sort"

// StagePrompt returns the embedded system prompt text for an LLM-backed stage,
// or "" for deterministic stages that have no prompt file. The LN2 fitter uses
// a hash of this text as the stage's prompt-identity key.
func StagePrompt(stageName string) string {
	switch stageName {
	case "code_writer":
		return codeWriterSystemPrompt
	case "test_generator":
		return testGeneratorSystemPrompt
	default:
		return ""
	}
}

// VerificationToolIdentities returns the sorted names of the deterministic
// verification tools in the default quality and security check sets. The LN2
// fitter hashes these into the run's tool fingerprint: a tool-set change opens
// a fresh calibration bucket.
func VerificationToolIdentities() []string {
	checks := append(append([]VerificationCheck(nil), DefaultQualityChecks()...), DefaultSecurityChecks()...)
	names := make([]string, 0, len(checks))
	for _, check := range checks {
		names = append(names, check.Name())
	}
	sort.Strings(names)
	return names
}
