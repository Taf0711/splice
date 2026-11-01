package splice

import (
	"encoding/json"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// VerificationReportKey is the canonical stage-output key for a verification
// report. A node whose capabilities declare produces_verification emits its
// report here, so the trajectory monitor collects verification without knowing
// which builtin produced it.
const VerificationReportKey = "verification_report"

// legacyVerificationKeys are the per-builtin keys that predate the canonical
// one. They stay because the lint and security severity counts classify by
// name, and the executor re-keys them to the canonical key so collection is
// universal.
var legacyVerificationKeys = []string{"static_analyzer_output", "security_auditor_output"}

// normalizeVerificationReport re-keys a verification-producing stage's legacy
// report to the canonical key. It is capability-aware: only a node whose
// capabilities declare produces_verification is re-keyed, so a node that merely
// happens to carry a report is not silently promoted into a verification
// authority.
func normalizeVerificationReport(output schemas.HarnessStageOutput, producesVerification bool) schemas.HarnessStageOutput {
	if !producesVerification || output.Data == nil {
		return output
	}
	if _, ok := output.Data[VerificationReportKey]; ok {
		return output
	}
	report, ok := legacyVerificationReport(output)
	if !ok {
		return output
	}
	cloned := make(map[string]any, len(output.Data)+1)
	for key, value := range output.Data {
		cloned[key] = value
	}
	cloned[VerificationReportKey] = report
	output.Data = cloned
	return output
}

// legacyVerificationReport returns the first legacy verification report on an
// output, in a stable key order.
func legacyVerificationReport(output schemas.HarnessStageOutput) (schemas.VerificationReport, bool) {
	for _, key := range legacyVerificationKeys {
		if report, ok := decodeVerificationReport(output.Data[key]); ok {
			return report, true
		}
	}
	return schemas.VerificationReport{}, false
}

// verificationReport returns the canonical report on an output, falling back to
// the legacy keys so an un-normalized legacy output still reads.
func verificationReport(output schemas.HarnessStageOutput) (schemas.VerificationReport, bool) {
	if output.Data == nil {
		return schemas.VerificationReport{}, false
	}
	if report, ok := decodeVerificationReport(output.Data[VerificationReportKey]); ok {
		return report, true
	}
	return legacyVerificationReport(output)
}

// decodeVerificationReport returns a typed report from a stage-output data
// value. A value that survived a JSON round trip is a map, not the typed
// struct, so a marshal round trip recovers it. The status must belong to the
// closed set, so unrelated data is never promoted into a verification
// authority.
func decodeVerificationReport(value any) (schemas.VerificationReport, bool) {
	switch typed := value.(type) {
	case schemas.VerificationReport:
		return typed, true
	case *schemas.VerificationReport:
		if typed == nil {
			return schemas.VerificationReport{}, false
		}
		return *typed, true
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return schemas.VerificationReport{}, false
		}
		var report schemas.VerificationReport
		if err := json.Unmarshal(encoded, &report); err != nil {
			return schemas.VerificationReport{}, false
		}
		switch report.Status {
		case schemas.VerificationPassed, schemas.VerificationFindings, schemas.VerificationIncomplete, schemas.VerificationNotApplicable:
			return report, true
		default:
			return schemas.VerificationReport{}, false
		}
	}
}
