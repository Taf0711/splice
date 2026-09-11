package warmcost

import (
	"fmt"
	"strings"
)

// renderMarkdown renders the aggregate as a human report. Every value is a
// field of the aggregate, never a recomputation.
func renderMarkdown(agg Aggregate) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Warm-cost measurement: %s\n\n", agg.RunID)
	fmt.Fprintf(&b, "Generated: %s\n\n", agg.GeneratedAt.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(&b, "Revision: `%s`  Sidecar: `%s`  Model: `%s`\n\n", agg.Provenance.BinaryRevision, agg.Provenance.SidecarRevision, agg.Provenance.ModelID)

	fmt.Fprintf(&b, "## Pre-registration\n\n")
	fmt.Fprintf(&b, "- Primary correctness endpoint: %s\n", agg.PreRegistration.PrimaryCorrectnessEndpoint)
	fmt.Fprintf(&b, "- Noninferiority margin: %g\n", agg.PreRegistration.NoninferiorityMargin)
	fmt.Fprintf(&b, "- Primary cost endpoint: %s\n", agg.PreRegistration.PrimaryCostEndpoint)
	fmt.Fprintf(&b, "- Secondary cost endpoint: %s\n", agg.PreRegistration.SecondaryCostEndpoint)
	fmt.Fprintf(&b, "- Win rule: %s\n", agg.PreRegistration.WinRule)
	fmt.Fprintf(&b, "- Stopping rule: %s\n\n", agg.PreRegistration.StoppingRule)

	fmt.Fprintf(&b, "## Cost coverage\n\n")
	fmt.Fprintf(&b, "Attempts: %d. Partial attempts: %d. Complete: %t.\n\n", agg.Coverage.Attempts, agg.Coverage.PartialAttempts, agg.Coverage.Complete)
	if !agg.Coverage.Complete {
		fmt.Fprintf(&b, "Total-cost claim withheld: at least one attempt has partial coverage.\n\n")
	}

	fmt.Fprintf(&b, "## Arms\n\n")
	fmt.Fprintf(&b, "| arm | attempts | verified | requests | req/verified | billed USD | USD/attempt | USD/verified | round share | cache share |\n")
	fmt.Fprintf(&b, "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	for _, m := range agg.ArmMetrics {
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %.2f | %.4f | %.4f | %.4f | %.3f | %.3f |\n",
			m.Arm, m.Attempts, m.VerifiedCompletions, m.Requests, m.RequestsPerVerifiedCompletion,
			m.BilledUSD, m.BilledUSDPerAttempt, m.BilledUSDPerVerifiedCompletion, m.RoundShare, m.CacheShare)
	}
	fmt.Fprintf(&b, "\n")

	fmt.Fprintf(&b, "## Cost decomposition (warm minus cold)\n\n")
	fmt.Fprintf(&b, "| channel | USD |\n| --- | ---: |\n")
	fmt.Fprintf(&b, "| total delta | %+.4f |\n", agg.Decomposition.TotalDeltaUSD)
	fmt.Fprintf(&b, "| round channel | %+.4f |\n", agg.Decomposition.RoundChannelUSD)
	fmt.Fprintf(&b, "| payload channel | %+.4f |\n", agg.Decomposition.PayloadChannelUSD)
	fmt.Fprintf(&b, "\nCache channel: %+d tokens. %s\n\n", agg.Decomposition.CacheTokensDelta, agg.Decomposition.CacheChannelNote)

	fmt.Fprintf(&b, "## Task-clustered bootstrap\n\n")
	fmt.Fprintf(&b, "Unit: %s. Samples: %d. Tasks: %d. Seed: %d.\n\n", agg.Bootstrap.Unit, agg.Bootstrap.Samples, agg.Bootstrap.Tasks, agg.Bootstrap.Seed)
	fmt.Fprintf(&b, "| delta | lower 2.5%% | upper 97.5%% |\n| --- | ---: | ---: |\n")
	fmt.Fprintf(&b, "| billed USD/attempt | %+.4f | %+.4f |\n", agg.Bootstrap.BilledUSDPerAttempt.Lower, agg.Bootstrap.BilledUSDPerAttempt.Upper)
	fmt.Fprintf(&b, "| requests/attempt | %+.3f | %+.3f |\n", agg.Bootstrap.RequestsPerAttempt.Lower, agg.Bootstrap.RequestsPerAttempt.Upper)
	fmt.Fprintf(&b, "| success rate | %+.3f | %+.3f |\n\n", agg.Bootstrap.SuccessRateDelta.Lower, agg.Bootstrap.SuccessRateDelta.Upper)

	fmt.Fprintf(&b, "## Claim\n\nAllowed: %t. %s\n\n", agg.Claim.TotalCostClaimAllowed, agg.Claim.Reason)

	fmt.Fprintf(&b, "## Per-task effects\n\n")
	fmt.Fprintf(&b, "| task | cold USD/attempt | warm USD/attempt | delta USD | delta requests | delta success |\n")
	fmt.Fprintf(&b, "| --- | ---: | ---: | ---: | ---: | ---: |\n")
	for _, e := range agg.PerTask {
		fmt.Fprintf(&b, "| %s | %.4f | %.4f | %+.4f | %+.2f | %+.2f |\n",
			e.TaskID, e.ColdBilledUSDPerAttempt, e.WarmBilledUSDPerAttempt, e.DeltaBilledUSDPerAttempt, e.DeltaRequestsPerAttempt, e.DeltaSuccessRate)
	}
	fmt.Fprintf(&b, "\n")
	return b.String()
}
