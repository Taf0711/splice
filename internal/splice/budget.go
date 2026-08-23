package splice

import (
	"fmt"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// stageBudgets holds per-stage baseline budgets.
//
// Light-tier derivation (run 3 measured data, splice-eval-taskset-run3):
// cold successes show prompt-in median 3.7k / p90 4.2k tokens per stage call;
// warm runs that aborted showed prompt-in median 18.3k / p90 23.7k because
// memory injection inflates composed input roughly 5x. InputMax 20000 sits
// above the warm p90 so a legitimate memory payload survives compaction
// instead of being stripped to nothing, while compaction (stage_input.go)
// still trims runaway bundles deterministically. OutputMax stays at 8192:
// successful-run output p90 is ~4.8k, so 8192 already carries ~1.7x headroom,
// and output overflow remains the runaway-generation tripwire.
func stageBudgets(tier schemas.PipelineTier) map[string]schemas.StageBudget {
	codeWriterInput := 20_000
	codeWriterOutput := 8_192
	if tier == schemas.TierTrivial {
		codeWriterInput = 2_000
		codeWriterOutput = 1_000
	}

	return map[string]schemas.StageBudget{
		"code_writer": {
			InputMax:  codeWriterInput,
			OutputMax: codeWriterOutput,
		},
		"test_generator": {
			InputMax:  3_000,
			OutputMax: 8_192,
			Skippable: true,
		},
		"static_analyzer": {
			InputMax:  0,
			OutputMax: 0,
			Skippable: true,
		},
		"security_auditor": {
			InputMax:  0,
			OutputMax: 0,
			Skippable: true,
		},
		"test_runner": {
			InputMax:  0,
			OutputMax: 0,
			Skippable: true,
		},
		"acceptance_verifier": {
			InputMax:  0,
			OutputMax: 0,
			Skippable: true,
		},
	}
}

// BudgetForTier returns a conservative initial budget for a pipeline tier.
func BudgetForTier(tier schemas.PipelineTier) (schemas.TokenBudget, error) {
	names, err := StageNamesForTier(tier)
	if err != nil {
		return schemas.TokenBudget{}, err
	}
	budgets := stageBudgets(tier)
	stages := make(map[string]schemas.StageBudget, len(names))
	for _, name := range names {
		stages[name] = budgets[name]
	}

	reserve := reserveForTier(tier)
	var totalInput, totalOutput int
	for _, b := range stages {
		totalInput += b.InputMax
		totalOutput += b.OutputMax
	}

	return schemas.TokenBudget{
		TotalInputBudget:  totalInput + reserve,
		TotalOutputBudget: totalOutput + reserve,
		PerStage:          stages,
		Reserve:           reserve,
		OverflowPolicy:    "abort",
	}, nil
}

// StageNamesForTier returns the live stage shape for a pipeline tier.
func StageNamesForTier(tier schemas.PipelineTier) ([]string, error) {
	switch tier {
	case schemas.TierTrivial:
		return []string{"code_writer"}, nil
	case schemas.TierLight:
		return []string{"code_writer", "static_analyzer", "test_runner", "acceptance_verifier"}, nil
	case schemas.TierStandard:
		return []string{"code_writer", "test_generator", "static_analyzer", "test_runner", "acceptance_verifier"}, nil
	case schemas.TierSubstantial, schemas.TierArchitectural:
		return []string{"code_writer", "test_generator", "static_analyzer", "security_auditor", "test_runner", "acceptance_verifier"}, nil
	default:
		return nil, fmt.Errorf("unknown pipeline tier %q", tier)
	}
}

func reserveForTier(tier schemas.PipelineTier) int {
	switch tier {
	case schemas.TierTrivial:
		return 500
	case schemas.TierLight:
		return 1_000
	case schemas.TierStandard:
		return 1_200
	default:
		return 1_500
	}
}
