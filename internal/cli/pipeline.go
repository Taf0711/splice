package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Taf0711/splice/internal/config"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

const pipelineUsage = `Usage: splice pipeline <command>

Commands:
  list                       list the topology layers and the active pipeline
  show [name|path]           show the nodes, edges, and command nodes of a topology
  use <name>                 set config.json's active_pipeline to a library entry
  export <name|path> [file]  write a topology to a file, or to stdout
  import <path|url> [--yes]  install a topology into the user library

Topology files: <workspace>/.splice/pipeline.json (project),
~/.config/splice/pipelines/<name>.json (library), or a path passed to
--pipeline. A command node is always listed by show and before an import.
An import over https is size-capped and refuses a cross-host redirect.
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
	case "use":
		return runPipelineUse(args[1:], stdout, stderr)
	case "export":
		return runPipelineExport(args[1:], stdout, stderr)
	case "import":
		return runPipelineImport(args[1:], stdout, stderr)
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
		if node.Model != nil {
			effort := node.Model.ReasoningEffort
			if effort == "" {
				effort = "default effort"
			}
			fmt.Fprintf(&b, "    model: %s/%s (%s)\n", node.Model.ProviderProfile, node.Model.Model, effort)
		}
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

// runPipelineUse sets config.json's active_pipeline to a library entry.
func runPipelineUse(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintf(stderr, "usage: splice pipeline use <name>\n")
		return 2
	}
	name := strings.TrimSpace(args[0])
	topology, path, err := splicerun.LoadNamedTopology(name)
	if err != nil {
		fmt.Fprintf(stderr, "use pipeline %q: %v\n", name, err)
		return 1
	}
	if err := setActivePipeline(name); err != nil {
		fmt.Fprintf(stderr, "use pipeline %q: %v\n", name, err)
		return 1
	}
	fmt.Fprintf(stdout, "active pipeline: %s (%s)\n", topology.Name, path)
	reportCommandNodes(stdout, topology)
	return 0
}

// runPipelineExport writes a topology to a file, or to stdout when no file is
// given.
func runPipelineExport(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintf(stderr, "usage: splice pipeline export <name|path> [file]\n")
		return 2
	}
	topology, path, err := splicerun.LoadNamedTopology(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "export: %v\n", err)
		return 1
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "export: %v\n", err)
		return 1
	}
	if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
		if _, err := stdout.Write(data); err != nil {
			fmt.Fprintf(stderr, "export: %v\n", err)
			return 1
		}
		return 0
	}
	target := strings.TrimSpace(args[1])
	if err := os.WriteFile(target, data, 0o644); err != nil {
		fmt.Fprintf(stderr, "export: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s (%s)\n", target, topology.Name)
	return 0
}

// runPipelineImport installs a topology from a file path or an https URL. It
// validates before writing, lists command nodes for consent, and requires
// confirmation unless --yes is given.
func runPipelineImport(args []string, stdout, stderr io.Writer) int {
	var (
		reference string
		assumeYes bool
	)
	for _, arg := range args {
		switch {
		case arg == "--yes" || arg == "-y":
			assumeYes = true
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(stderr, "unknown import flag %q\n", arg)
			return 2
		case reference == "":
			reference = strings.TrimSpace(arg)
		default:
			fmt.Fprintf(stderr, "unexpected argument %q\n", arg)
			return 2
		}
	}
	if reference == "" {
		fmt.Fprintf(stderr, "usage: splice pipeline import <path|url> [--yes]\n")
		return 2
	}
	data, source, err := readTopologyReference(reference)
	if err != nil {
		fmt.Fprintf(stderr, "import %s: %v\n", reference, err)
		return 1
	}
	var topology schemas.PipelineTopology
	if err := json.Unmarshal(data, &topology); err != nil {
		fmt.Fprintf(stderr, "import %s: parse: %v\n", source, err)
		return 1
	}
	if err := topology.Validate(); err != nil {
		fmt.Fprintf(stderr, "import %s: %v\n", source, err)
		return 1
	}
	fmt.Fprintf(stdout, "import %s: topology %q with %d node(s)\n", source, topology.Name, len(topology.Nodes))
	reportCommandNodes(stdout, &topology)
	if !assumeYes {
		if !promptYesNo(os.Stdin, stdout, fmt.Sprintf("Install %q into the user library?", topology.Name)) {
			fmt.Fprintln(stdout, "import cancelled")
			return 0
		}
	}
	target, err := libraryEntryPath(topology.Name)
	if err != nil {
		fmt.Fprintf(stderr, "import: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		fmt.Fprintf(stderr, "import: %v\n", err)
		return 1
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		fmt.Fprintf(stderr, "import: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "installed %s\n", target)
	return 0
}

// readTopologyReference reads a topology from a file path or an https URL.
func readTopologyReference(reference string) ([]byte, string, error) {
	if isTopologyURL(reference) {
		data, err := fetchTopology(reference)
		return data, reference, err
	}
	data, err := os.ReadFile(reference)
	return data, reference, err
}

func isTopologyURL(reference string) bool {
	return strings.HasPrefix(reference, "http://") || strings.HasPrefix(reference, "https://")
}

// maxTopologyImportBytes caps a remote topology so an import cannot stream
// unbounded data into memory.
const maxTopologyImportBytes = 1 << 20

// topologyImportClient fetches remote topologies. The call site restricts the
// scheme to https; the client refuses a cross-host redirect and has a timeout.
var topologyImportClient = &http.Client{
	Timeout:       15 * time.Second,
	CheckRedirect: checkTopologyRedirect,
}

// checkTopologyRedirect refuses a redirect that leaves the original host, so a
// trusted URL cannot bounce an import to another origin.
func checkTopologyRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	if req.URL.Host != via[0].URL.Host {
		return fmt.Errorf("refusing cross-host redirect to %s", req.URL.Host)
	}
	if len(via) >= 3 {
		return fmt.Errorf("refusing more than 3 redirects")
	}
	return nil
}

func fetchTopology(rawURL string) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	if parsed.Scheme != "https" {
		return nil, fmt.Errorf("remote import requires https, got %q", parsed.Scheme)
	}
	resp, err := topologyImportClient.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTopologyImportBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxTopologyImportBytes {
		return nil, fmt.Errorf("topology exceeds the %d-byte import cap", maxTopologyImportBytes)
	}
	return data, nil
}

// libraryEntryPath returns the user-library path for a pipeline name.
func libraryEntryPath(name string) (string, error) {
	base, err := config.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "splice", "pipelines", name+".json"), nil
}

// setActivePipeline writes config.json's active_pipeline while preserving every
// other key, so a topology selection never drops unrelated configuration.
func setActivePipeline(name string) error {
	base, err := config.UserConfigDir()
	if err != nil {
		return err
	}
	path := filepath.Join(base, "splice", "config.json")
	raw := map[string]json.RawMessage{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	encoded, err := json.Marshal(name)
	if err != nil {
		return err
	}
	raw["active_pipeline"] = encoded
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o600)
}

// reportCommandNodes lists the executable command nodes of a topology, so a
// command is visible before it is installed or activated.
func reportCommandNodes(w io.Writer, topology *schemas.PipelineTopology) {
	commandNodes := 0
	for _, node := range topology.Nodes {
		if node.Type != schemas.NodeTypeCommand {
			continue
		}
		commandNodes++
		fmt.Fprintf(w, "  command node %s: %s\n", node.Name, strings.Join(node.Command, " "))
	}
	if commandNodes == 0 {
		return
	}
	fmt.Fprintf(w, "  %d command node(s) will run shell commands when this topology is active\n", commandNodes)
}

// promptYesNo reads one line from reader and reports whether it is affirmative.
func promptYesNo(reader io.Reader, w io.Writer, prompt string) bool {
	fmt.Fprintf(w, "%s [y/N] ", prompt)
	line, err := bufio.NewReader(reader).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}
