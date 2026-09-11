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
