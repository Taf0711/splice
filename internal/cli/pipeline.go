package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

const pipelineUsage = `Usage: splice pipeline <command>

Commands:
  list                 list the topology layers and the active pipeline
  show [name|path]     show the nodes, edges, and command nodes of a topology

Topology files: <workspace>/.splice/pipeline.json (project),
~/.config/splice/pipelines/<name>.json (library), or a path passed to
--pipeline. A command node is always listed by show and before an import.
`

// runPipelineCommand implements `splice pipeline <verb>`.
func runPipelineCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, pipelineUsage)
		return 0
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, pipelineUsage)
		return 0
	case "list":
		return runPipelineList(stdout, stderr)
	case "show":
		return runPipelineShow(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown pipeline subcommand %q\n\n%s", args[0], pipelineUsage)
		return 2
	}
}

func runPipelineList(stdout, stderr io.Writer) int {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "resolve working directory: %v\n", err)
		return 1
	}
	sources := splicerun.TopologySourcesFor(root, workspaceTrusted(root))
	project, flag, library, err := sources.Sources()
	if err != nil {
		fmt.Fprintf(stderr, "resolve topology sources: %v\n", err)
		return 1
	}
	topology, source, warnings, err := splicerun.ResolveTopology(sources)
	if err != nil {
		fmt.Fprintf(stderr, "resolve topology: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "active: %s (%s)\n", topology.Name, source)
	if project != "" {
		fmt.Fprintf(stdout, "project: %s%s\n", project, presenceSuffix(project))
	}
	if flag != "" {
		fmt.Fprintf(stdout, "flag: %s%s\n", flag, presenceSuffix(flag))
	}
	libraryDir := filepath.Join(sources.UserConfigDir, "splice", "pipelines")
	fmt.Fprintf(stdout, "library: %s\n", libraryDir)
	entries, readErr := os.ReadDir(libraryDir)
	if readErr == nil {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
				continue
			}
			names = append(names, strings.TrimSuffix(entry.Name(), ".json"))
		}
		sort.Strings(names)
		for _, name := range names {
			marker := ""
			if library != "" && filepath.Base(library) == name+".json" {
				marker = " (active)"
			}
			fmt.Fprintf(stdout, "  %s%s\n", name, marker)
		}
	}
	for _, warning := range warnings {
		fmt.Fprintf(stderr, "warning: %s\n", warning)
	}
	return 0
}

func runPipelineShow(args []string, stdout, stderr io.Writer) int {
	root, _ := os.Getwd()
	var topology *schemas.PipelineTopology
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		loaded, path, err := splicerun.LoadNamedTopology(args[0])
		if err != nil {
			fmt.Fprintf(stderr, "load topology %q: %v\n", args[0], err)
			return 1
		}
		topology = loaded
		fmt.Fprintf(stdout, "source: %s\n", path)
	} else {
		resolved, source, _, err := splicerun.ResolveTopology(splicerun.TopologySourcesFor(root, workspaceTrusted(root)))
		if err != nil {
			fmt.Fprintf(stderr, "resolve topology: %v\n", err)
			return 1
		}
		topology = resolved
		fmt.Fprintf(stdout, "source: %s\n", source)
	}
	fmt.Fprint(stdout, formatTopology(topology))
	return 0
}

// workspaceTrusted resolves the workspace trust without prompting. A resolver
// failure is treated as untrusted, which keeps a project topology out of the
// active set.
func workspaceTrusted(root string) bool {
	trusted, _, _, _, err := resolveWorkspaceTrust(root, "", false, false)
	return err == nil && trusted
}

func presenceSuffix(path string) string {
	if _, err := os.Stat(path); err == nil {
		return " (present)"
	}
	return " (absent)"
}

// formatTopology renders a topology's nodes, edges, and command nodes. Command
// nodes are always listed, so an executable command is visible before it runs.
func formatTopology(topology *schemas.PipelineTopology) string {
	var b strings.Builder
	fmt.Fprintf(&b, "topology: %s\n", topology.Name)
	if topology.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", topology.Description)
	}
	b.WriteString("nodes:\n")
	for _, node := range topology.Nodes {
		caps := node.EffectiveCapabilities()
		kind := "model"
		if caps.ModelFree {
			kind = "model-free"
		}
		tiers := "all tiers"
		if len(node.Tiers) > 0 {
			parts := make([]string, 0, len(node.Tiers))
			for _, tier := range node.Tiers {
				parts = append(parts, string(tier))
			}
			tiers = strings.Join(parts, ",")
		}
		fmt.Fprintf(&b, "  %s (%s, %s, %s)\n", node.Name, node.Type, kind, tiers)
	}
	b.WriteString("edges:\n")
	if len(topology.Edges) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, edge := range topology.Edges {
		fmt.Fprintf(&b, "  %s -> %s (%s)\n", edge.From, edge.To, edge.Payload.Effective())
	}
	b.WriteString("command nodes:\n")
	commandNodes := 0
	for _, node := range topology.Nodes {
		if node.Type != schemas.NodeTypeCommand {
			continue
		}
		commandNodes++
		fmt.Fprintf(&b, "  %s: %s\n", node.Name, strings.Join(node.Command, " "))
	}
	if commandNodes == 0 {
		b.WriteString("  (none)\n")
	}
	return b.String()
}
