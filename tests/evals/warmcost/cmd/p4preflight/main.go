// Command p4preflight resolves the model a pipeline stage will actually use -
// per-stage stage-models.json override, then the file Default, then the active
// provider model - and fails loud when it is not the intended MODEL. A printed
// --model flag is not the resolved route: the operator's stage-models.json
// mapped code_writer to gpt-5.6-sol and spent $0.1648 under an intended
// z-ai/glm-5.3-flash run before this check existed.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Taf0711/splice/tests/evals/warmcost"
)

func main() {
	spliceDir := flag.String("splice-dir", "", "splice config dir (default: $XDG_CONFIG_HOME/splice or ~/.config/splice)")
	stage := flag.String("stage", "code_writer", "pipeline stage to resolve")
	want := flag.String("want", "", "intended model; a mismatch aborts")
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
}
