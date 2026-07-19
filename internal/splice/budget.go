package splice

import (
	"fmt"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// stageBudgets holds per-stage baseline budgets.
func stageBudgets(tier schemas.PipelineTier) map[string]schemas.StageBudget {
	codeWriterInput := 4_000
	codeWriterOutput := 8_192
	if tier == schemas.TierTrivial {
		codeWriterInput = 2_000
		codeWriterOutput = 1_000
	}

	return map[string]schemas.StageBudget{
		"code_writer": {
			InputMax:  codeWriterInput,
			OutputMax: codeWriterOutput,
			ModelTier: "medium",
		},
		"test_generator": {
			InputMax:  3_000,
			OutputMax: 8_192,
			ModelTier: "medium",
			Skippable: true,
		},
		"static_analyzer": {
			InputMax:  0,
			OutputMax: 0,
			ModelTier: "",
			Skippable: true,
		},
		"security_auditor": {
			InputMax:  0,
			OutputMax: 0,
			ModelTier: "",
			Skippable: true,
		},
		"test_runner": {
			InputMax:  0,
			OutputMax: 0,
			ModelTier: "",
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
		return []string{"code_writer", "static_analyzer", "test_runner"}, nil
	case schemas.TierStandard:
		return []string{"code_writer", "test_generator", "static_analyzer", "test_runner"}, nil
	case schemas.TierSubstantial, schemas.TierArchitectural:
		return []string{"code_writer", "test_generator", "static_analyzer", "security_auditor", "test_runner"}, nil
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
