package v2

import (
	"fmt"
	"hash/fnv"
	"math/rand/v2"
)

// GenerateSchedule creates the deterministic Latin-square schedule consumed by
// Manifest.Schedule and later by the EV2 runner. The protocol seed and
// experiment ID are the only randomization inputs.
func GenerateSchedule(p Protocol, taskIDs []string) (Schedule, error) {
	if err := p.Validate(); err != nil {
		return Schedule{}, fmt.Errorf("protocol: %w", err)
	}
	if err := validateScheduleTaskIDs(p, taskIDs); err != nil {
		return Schedule{}, err
	}

	armCount := len(p.Arms)
	rng := rand.New(rand.NewPCG(uint64(p.Seed), experimentSeedWord(p.ExperimentID)))
	baseArms := rng.Perm(armCount)

	trials := make([]TrialSpec, 0, len(taskIDs)*armCount*p.Repetitions)
	for repetition := 1; repetition <= p.Repetitions; repetition++ {
		taskOrder := rng.Perm(len(taskIDs))
		for row, taskIndex := range taskOrder {
			environmentBlock := row + 1
			for position := 0; position < armCount; position++ {
				armIndex := baseArms[(position+row+repetition-1)%armCount]
				trials = append(trials, TrialSpec{Key: TrialKey{
					ExperimentID:     p.ExperimentID,
					TaskID:           taskIDs[taskIndex],
					Arm:              p.Arms[armIndex].Name,
					RepetitionID:     repetition,
					EnvironmentBlock: environmentBlock,
				}})
			}
		}
	}

	schedule := Schedule{Trials: trials}
	for i, trial := range schedule.Trials {
		if err := trial.ValidateFor(p); err != nil {
			return Schedule{}, fmt.Errorf("generated trials[%d]: %w", i, err)
		}
	}
	if err := schedule.CompleteFor(p, taskIDs); err != nil {
		return Schedule{}, fmt.Errorf("generated schedule: %w", err)
	}
	return schedule, nil
}

func validateScheduleTaskIDs(p Protocol, taskIDs []string) error {
	armNames := make(map[string]bool, len(p.Arms))
	for _, arm := range p.Arms {
		armNames[arm.Name] = true
	}
	seen := make(map[string]bool, len(taskIDs))
	for i, taskID := range taskIDs {
		if taskID == "" {
			return fmt.Errorf("taskIDs[%d] is empty", i)
		}
		if armNames[taskID] {
			return fmt.Errorf("taskIDs[%d] %q collides with an arm name", i, taskID)
		}
		if seen[taskID] {
			return fmt.Errorf("taskIDs[%d] duplicates task ID %q", i, taskID)
		}
		seen[taskID] = true
	}
	return nil
}

func experimentSeedWord(experimentID string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(experimentID))
	return h.Sum64()
}
