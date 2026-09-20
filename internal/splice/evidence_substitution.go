package splice

import (
	"fmt"
	"os"
	"strings"
)

// EvidenceSubstitutionEnvVar is the explicit opt-in for production evidence
// substitution. The measured effect is a net cost: an admitted substitution
// delivers evidence and spends a validation read, while the eliminated
// operations are prompt-neutral and the model-visible context and tool use do
// not change. The path is therefore OFF by default. Set it to "on" only for a
// controlled experiment or for a future revision that demonstrates a win.
const EvidenceSubstitutionEnvVar = "SPLICE_EVIDENCE_SUBSTITUTION"

// resolveEvidenceSubstitution reads the opt-in. Unset or empty means OFF: no
// evidence plan is built and no validation read is spent. An invalid value
// fails loud, so a misspelled experiment cannot silently measure the cold path.
func resolveEvidenceSubstitution() (bool, error) {
	raw := strings.TrimSpace(os.Getenv(EvidenceSubstitutionEnvVar))
	switch strings.ToLower(raw) {
	case "", "off":
		return false, nil
	case "on":
		return true, nil
	default:
		return false, fmt.Errorf("%s: invalid value %q (want on or off)", EvidenceSubstitutionEnvVar, raw)
	}
}

// MemoryPrefetchEnvVar is the explicit opt-in for memory-assisted
// dependency prefetch: when a retained verified record names a source
// file, the host fetches that file's current bounded view into the initial
// context handshake, so the model starts with the declaration instead of
// spending expansion rounds to rediscover it. The selection rule is
// uncalibrated (delivery is capped and the trigger is record delivery,
// not a measured expected value), so this is an experimental treatment,
// OFF by default, and never reported as a substitution: no operation is
// removed, source is acquired earlier.
const MemoryPrefetchEnvVar = "SPLICE_MEMORY_PREFETCH"

// resolveMemoryPrefetch reads the opt-in. Unset or empty means OFF. An
// invalid value fails loud, so a misspelled experiment cannot silently
// measure the cold path.
func resolveMemoryPrefetch() (bool, error) {
	raw := strings.TrimSpace(os.Getenv(MemoryPrefetchEnvVar))
	switch strings.ToLower(raw) {
	case "", "off":
		return false, nil
	case "on":
		return true, nil
	default:
		return false, fmt.Errorf("%s: invalid value %q (want on or off)", MemoryPrefetchEnvVar, raw)
	}
}
