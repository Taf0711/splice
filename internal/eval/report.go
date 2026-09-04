package eval

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ReportContractVersion is the paired-eval report contract.
const ReportContractVersion = "splice.eval.pe.v1"

// TaskPair is one task's paired outcome.
type TaskPair struct {
	Name string `json:"name"`
	// Attempt is the 1-based rollout number for this pair. With one rollout
	// it is always 1; with N rollouts the same task name appears N times,
	// once per attempt, and aggregations derive per-attempt rows (section
	// 31: raw first, summaries second).
	Attempt           int  `json:"attempt,omitempty"`
	ColdSuccess       bool `json:"cold_success"`
	WarmSuccess       bool `json:"warm_success"`
	ColdTokens        int  `json:"cold_tokens"`
	WarmTokens        int  `json:"warm_tokens"`
	ColdInterventions int  `json:"cold_interventions,omitempty"`
	WarmInterventions int  `json:"warm_interventions,omitempty"`
	// ColdTelemetry and WarmTelemetry record whether a matching usage trace
	// was found for that arm's run. False next to a success means the token
	// count is absent data, not a measured zero.
	ColdTelemetry bool `json:"cold_telemetry"`
	WarmTelemetry bool `json:"warm_telemetry"`
	// ColdError and WarmError carry a verbatim exec failure for that arm.
	// A failed exec counts as arm failure with whatever partial tokens were
	// reported, so an abort biases no gate in anyone's favor.
	ColdError string `json:"cold_error,omitempty"`
	WarmError string `json:"warm_error,omitempty"`
}

// Report is the paired-eval result. It carries counts, ratios, and named
// thresholds only: no p-values or statistics machinery.
type Report struct {
	Contract  string       `json:"contract"`
	Taskset   string       `json:"taskset"`
	Model     string       `json:"model,omitempty"`
	Provider  string       `json:"provider,omitempty"`
	Timestamp string       `json:"timestamp"`
	Pairs     int          `json:"pairs"`
	Cold      ArmStats     `json:"cold"`
	Warm      ArmStats     `json:"warm"`
	Tasks     []TaskPair   `json:"tasks"`
	Gates     []GateResult `json:"gates"`
	Verdict   string       `json:"verdict"`
	Reason    string       `json:"reason"`
	Constants Constants    `json:"constants"`
}

// Constants is the named-threshold block so a decision is auditable.
type Constants struct {
	PairFloor        int     `json:"pair_floor"`
	CostMargin       float64 `json:"cost_margin"`
	SuccessTolerance int     `json:"success_tolerance"`
}

// ReportConstants returns the fixed gate constants in report shape.
func ReportConstants() Constants {
	return Constants{PairFloor: PairFloor, CostMargin: CostMargin, SuccessTolerance: SuccessTolerance}
}

// WriteJSON writes the report as indented JSON to out.
func (r Report) WriteJSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// RenderMarkdown renders the human report.
func (r Report) RenderMarkdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Paired eval: %s\n\n", r.Taskset)
	fmt.Fprintf(&b, "model: %s\n", orDash(r.Model))
	fmt.Fprintf(&b, "provider: %s\n", orDash(r.Provider))
	fmt.Fprintf(&b, "timestamp: %s\n\n", r.Timestamp)

	fmt.Fprintf(&b, "## Verdict\n\n**%s** — %s\n\n", r.Verdict, r.Reason)

	fmt.Fprintf(&b, "## Gates\n\n")
	fmt.Fprintf(&b, "| gate | outcome |\n| --- | --- |\n")
	for _, gate := range r.Gates {
		outcome := "pass"
		reason := ""
		if !gate.Passed {
			outcome = "fail"
			reason = " — " + gate.Reason
		}
		fmt.Fprintf(&b, "| %s | %s%s |\n", gate.Name, outcome, reason)
	}

	fmt.Fprintf(&b, "\n## Tasks\n\n")
	fmt.Fprintf(&b, "| task | cold success | warm success | cold tokens | warm tokens |\n| --- | --- | --- | --- | --- |\n")
	for _, task := range r.Tasks {
		fmt.Fprintf(&b, "| %s | %t | %t | %d | %d |\n", task.Name, task.ColdSuccess, task.WarmSuccess, task.ColdTokens, task.WarmTokens)
	}

	var missingTelemetry []string
	for _, task := range r.Tasks {
		if task.ColdSuccess && !task.ColdTelemetry {
			missingTelemetry = append(missingTelemetry, task.Name+" (cold)")
		}
		if task.WarmSuccess && !task.WarmTelemetry {
			missingTelemetry = append(missingTelemetry, task.Name+" (warm)")
		}
	}
	if len(missingTelemetry) > 0 {
		fmt.Fprintf(&b, "\n> **TELEMETRY WARNING:** no usage trace was found for %s. "+
			"Their token counts are absent data, not measured zeros, so cost numbers are unreliable.\n",
			strings.Join(missingTelemetry, ", "))
	}

	fmt.Fprintf(&b, "\n## Arms\n\n")
	fmt.Fprintf(&b, "cold: %d successes, %d tokens, %d weighted interventions\n", r.Cold.Successes, r.Cold.Tokens, r.Cold.WeightedInterventions)
	fmt.Fprintf(&b, "warm: %d successes, %d tokens, %d weighted interventions\n", r.Warm.Successes, r.Warm.Tokens, r.Warm.WeightedInterventions)

	var failed []TaskPair
	for _, task := range r.Tasks {
		if task.ColdError != "" || task.WarmError != "" {
			failed = append(failed, task)
		}
	}
	if len(failed) > 0 {
		fmt.Fprintf(&b, "\n## Run Errors\n\n")
		fmt.Fprintf(&b, "Exec failures count as arm failures with their partial tokens.\n\n")
		for _, task := range failed {
			if task.ColdError != "" {
				fmt.Fprintf(&b, "- %s (cold): %s\n", task.Name, singleLine(task.ColdError))
			}
			if task.WarmError != "" {
				fmt.Fprintf(&b, "- %s (warm): %s\n", task.Name, singleLine(task.WarmError))
			}
		}
	}

	fmt.Fprintf(&b, "\n## Constants\n\n")
	fmt.Fprintf(&b, "pair floor: %d\n", r.Constants.PairFloor)
	fmt.Fprintf(&b, "cost margin: %.2f\n", r.Constants.CostMargin)
	fmt.Fprintf(&b, "success tolerance: %d\n", r.Constants.SuccessTolerance)
	return b.String()
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// singleLine flattens a captured exec transcript to one report line so a
// multi-thousand-token stream-json dump cannot blow up the markdown table
// layout. The full text still travels in pe-report.json.
func singleLine(s string) string {
	flat := strings.Join(strings.Fields(s), " ")
	const max = 400
	if len(flat) > max {
		return flat[:max] + " ... [truncated; see pe-report.json]"
	}
	return flat
}
