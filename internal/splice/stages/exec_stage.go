package stages

// exec_stage.go is the single construction point for every deterministic
// stage subprocess. All Tier-1.4 exec sites route through PrepareStageCommand,
// which enforces the fixed-binary allowlist at the procrun chokepoint and, when
// a stage sandbox engine is attached, scopes the filesystem to the workspace
// and denies network. A nil engine still enforces the allowlist and emits an
// audit record; it only skips OS-level confinement.

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/Taf0711/splice/internal/sandbox"
	"github.com/Taf0711/splice/internal/sandbox/procrun"
)

// PrepareStageCommand builds the exec.Cmd for one deterministic stage
// subprocess under the splice.stage profile. The command's first element must
// name a binary on the procrun stage allowlist; anything else is refused with
// an error naming the offending binary.
func PrepareStageCommand(ctx context.Context, sandboxEngine *sandbox.Engine, dir string, command []string) (*exec.Cmd, sandbox.CommandPlan, error) {
	if len(command) == 0 {
		return nil, sandbox.CommandPlan{}, fmt.Errorf("stage command is empty")
	}
	spec := sandbox.CommandSpec{
		Name: command[0],
		Args: append([]string(nil), command[1:]...),
		Dir:  dir,
	}
	prepared, err := procrun.Prepare(ctx, procrun.Request{
		ProfileID:       procrun.ProfileSpliceStage,
		AllowedBinaries: procrun.StageBinaries,
		Engine:          sandboxEngine,
		Spec:            spec,
	})
	if err != nil {
		return nil, sandbox.CommandPlan{}, err
	}
	return prepared.Cmd, prepared.Plan, nil
}
