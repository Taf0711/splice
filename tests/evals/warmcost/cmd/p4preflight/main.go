// Command p4preflight resolves the model a pipeline stage will actually use -
// per-stage stage-models.json override, then the file Default, then the active
// provider model - and fails loud when it is not the intended MODEL. A printed
// --model flag is not the resolved route: the operator's stage-models.json
// mapped code_writer to gpt-5.6-sol and spent $0.1648 under an intended
// z-ai/glm-5.3-flash run before this check existed.
//
// --check-reasoning additionally resolves the stage's reasoning effort from the
// same config and asserts the provider actually advertises it. The fam-05
// automatic arm proved the failure mode: the pinned medium was not supported
// by z-ai/glm-5.3-flash (advertised: max/high/low), the provider silently used
// high, and the arm burned the per-attempt bound without submitting. This check
// aborts before any provider request instead of spending on a misconfigured
// model.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Taf0711/splice/tests/evals/warmcost"
)

func main() {
	spliceDir := flag.String("splice-dir", "", "splice config dir (default: $XDG_CONFIG_HOME/splice or ~/.config/splice)")
	stage := flag.String("stage", "code_writer", "pipeline stage to resolve")
	want := flag.String("want", "", "intended model; a mismatch aborts")
	checkReasoning := flag.Bool("check-reasoning", false, "assert the resolved reasoning effort is advertised by the provider")
	modelsURL := flag.String("models-url", warmcost.DefaultOpenRouterModelsURL, "provider model catalog URL")
	allowUnverified := flag.Bool("allow-unverified-reasoning", false, "continue when the reasoning-effort check cannot run (default: abort, unknown is not zero)")
	flag.Parse()

	dir := *spliceDir
	if dir == "" {
		resolved, err := warmcost.ConfigDir("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "p4preflight:", err)
			os.Exit(2)
		}
		dir = resolved
	} else if filepath.Base(dir) != "splice" {
		// Accept an XDG base too, so callers cannot point at the wrong level
		// and silently resolve nothing.
		dir = filepath.Join(dir, "splice")
	}

	res, err := warmcost.ResolveStageModel(dir, *stage)
	if err != nil {
		fmt.Fprintf(os.Stderr, "p4preflight: resolve %s in %s: %v\n", *stage, dir, err)
		os.Exit(2)
	}
	fmt.Printf("p4preflight: stage=%s resolved_model=%s source=%s splice_dir=%s want=%s\n",
		res.Stage, res.Model, res.Source, dir, *want)
	if *want != "" && res.Model != *want {
		fmt.Fprintf(os.Stderr, "MODEL ASSERTION FAILED: stage %s resolves to %q (%s), want %q; aborting before any provider request\n",
			res.Stage, res.Model, res.Source, *want)
		os.Exit(1)
	}

	if !*checkReasoning {
		return
	}
	effort := warmcost.ResolveStageReasoningEffort(dir, *stage)
	if strings.TrimSpace(effort.Effort) == "" {
		// No effort is pinned: the provider default applies and there is
		// nothing to assert. Report it and continue so unconfigured
		// tasksets keep working; a PINNED effort is always asserted.
		fmt.Fprintf(os.Stderr, "p4preflight: stage %s reasoning_effort unset (%s); provider default applies, not asserted\n", *stage, effort.Source)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	info, err := warmcost.FetchModelReasoningInfo(ctx, *modelsURL, res.Model, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "REASONING EFFORT CHECK UNAVAILABLE: %v\n", err)
		if !*allowUnverified {
			fmt.Fprintln(os.Stderr, "aborting before any provider request (pass --allow-unverified-reasoning to override)")
			os.Exit(1)
		}
		return
	}
	if !warmcost.EffortSupported(effort.Effort, info.SupportedEfforts) {
		fmt.Fprintf(os.Stderr, "REASONING EFFORT ASSERTION FAILED: model %s advertises supported_efforts=%v (default %q); stage %s pins %q (%s); aborting before any provider request\n",
			res.Model, info.SupportedEfforts, info.DefaultEffort, *stage, effort.Effort, effort.Source)
		os.Exit(1)
	}
	fmt.Printf("p4preflight: stage=%s reasoning_effort=%s source=%s supported_efforts=%v model=%s\n",
		*stage, effort.Effort, effort.Source, info.SupportedEfforts, res.Model)
}
