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
	"github.com/Taf0711/splice/internal/web"
)

const pipelineUsage = `Usage: splice pipeline <command>

Commands:
  list                       list the topology layers and the active pipeline
  show [name|path|default]   show the nodes, edges, and command nodes of a topology
  use <name>                 set config.json's active_pipeline to a library entry
  export <name|path|default> [file]  write a topology to a file, or to stdout
  import <path|url> [--yes]  install a topology into the user library
  edit [--new] [name|default]  edit a topology in the browser editor

Topology files: <workspace>/.splice/pipeline.json (project),
~/.config/splice/pipelines/<name>.json (library), or a path passed to
--pipeline. A command node is always listed by show and before an import.
An import over https is size-capped and refuses a cross-host redirect.
edit serves the browser editor on 127.0.0.1 and prints a one-time link to
stderr. It edits a draft and publishes to the library, never to a project.
`

// pipelineStdin is where pipeline import reads its confirmation. Tests replace it.
var pipelineStdin io.Reader = os.Stdin

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
	case "edit":
		return runPipelineEdit(args[1:], stderr)
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
	if len(args) > 0 && strings.TrimSpace(args[0]) == "default" {
		topology = splicerun.DefaultTopology()
		fmt.Fprintf(stdout, "source: %s\n", splicerun.TopologySourceDefault)
	} else if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
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
	fmt.Fprint(stdout, splicerun.FormatTopology(topology))
	warnPinProfileGaps(stderr, topology, root)
	return 0
}

// warnPinProfileGaps warns for node pins whose provider profile is missing from
// the user's config. It warns only: profiles can differ per machine.
func warnPinProfileGaps(stderr io.Writer, topology *schemas.PipelineTopology, root string) {
	opts, err := config.WorkspaceResolveOptions(root, workspaceTrusted(root))
	if err != nil {
		fmt.Fprintf(stderr, "warning: cannot check node pin profiles: %v\n", err)
		return
	}
	resolved, err := config.Resolve(opts)
	if err != nil {
		fmt.Fprintf(stderr, "warning: cannot check node pin profiles: %v\n", err)
		return
	}
	for _, gap := range splicerun.PinProfileGaps(topology, resolved.Providers) {
		fmt.Fprintf(stderr, "warning: %s\n", gap)
	}
}

func presenceSuffix(path string) string {
	if _, err := os.Stat(path); err == nil {
		return " (present)"
	}
	return " (absent)"
}

// runPipelineUse sets config.json's active_pipeline to a library entry.
func runPipelineUse(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintf(stderr, "usage: splice pipeline use <name>\n")
		return 2
	}
	name := strings.TrimSpace(args[0])
	topology, path, err := splicerun.ActivateLibraryPipeline(name)
	if err != nil {
		fmt.Fprintf(stderr, "use pipeline %q: %v\n", name, err)
		return 1
	}
	fmt.Fprintf(stdout, "active pipeline: %s (%s)\n", topology.Name, path)
	reportCommandNodes(stdout, topology)
	root, _ := os.Getwd()
	warnPinProfileGaps(stderr, topology, root)
	return 0
}

// runPipelineExport writes a topology to a file, or to stdout when no file is
// given.
func runPipelineExport(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		fmt.Fprintf(stderr, "usage: splice pipeline export <name|path> [file]\n")
		return 2
	}
	var (
		data []byte
		name string
	)
	if strings.TrimSpace(args[0]) == "default" {
		encoded, err := json.MarshalIndent(splicerun.DefaultTopology(), "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "export: %v\n", err)
			return 1
		}
		data, name = append(encoded, '\n'), "default"
	} else {
		topology, path, err := splicerun.LoadNamedTopology(args[0])
		if err != nil {
			fmt.Fprintf(stderr, "export: %v\n", err)
			return 1
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(stderr, "export: %v\n", err)
			return 1
		}
		data, name = raw, topology.Name
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
	fmt.Fprintf(stdout, "wrote %s (%s)\n", target, name)
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
	topology, err := splicerun.DecodeTopology(data)
	if err != nil {
		fmt.Fprintf(stderr, "import %s: %v\n", source, err)
		return 1
	}
	fmt.Fprintf(stdout, "import %s: topology %q with %d node(s)\n", source, topology.Name, len(topology.Nodes))
	reportCommandNodes(stdout, &topology)
	// Validate passed, so the name is a safe library file name.
	target, err := splicerun.LibraryEntryPath(topology.Name)
	if err != nil {
		fmt.Fprintf(stderr, "import: %v\n", err)
		return 1
	}
	_, statErr := os.Stat(target)
	exists := statErr == nil
	if !assumeYes {
		question := fmt.Sprintf("Install %q into the user library?", topology.Name)
		if exists {
			question = fmt.Sprintf("Replace the existing library entry %q (%s)?", topology.Name, target)
		}
		if !promptYesNo(pipelineStdin, stdout, question) {
			fmt.Fprintln(stdout, "import cancelled")
			return 0
		}
	} else if exists {
		fmt.Fprintf(stdout, "replacing the existing library entry %s\n", target)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		fmt.Fprintf(stderr, "import: %v\n", err)
		return 1
	}
	if err := splicerun.WriteFileAtomic(target, data, 0o644); err != nil {
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

// reportCommandNodes lists the executable command nodes of a topology.
func reportCommandNodes(w io.Writer, topology *schemas.PipelineTopology) {
	for _, line := range splicerun.CommandNodeLines(topology) {
		fmt.Fprintln(w, line)
	}
	for _, line := range splicerun.AgentNodeLines(topology) {
		fmt.Fprintln(w, line)
	}
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

// pipelineEditContext ends a `pipeline edit` server. The default ends on
// Ctrl-C. Tests replace it.
var pipelineEditContext = signalContext

// runPipelineEdit serves the browser editor for a headless user. It opens a
// draft of the library entry name, a copy of the embedded default, or with
// no name the active topology. It resumes the latest draft of that source;
// --new starts a fresh one. The one-time link goes to stderr.
// The server runs until Ctrl-C or until it is idle for the idle timeout.
func runPipelineEdit(args []string, stderr io.Writer) int {
	ref, fresh, err := web.ParseEditArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "pipeline edit: %v\nusage: splice pipeline edit [--new] [name|default]\n", err)
		return 2
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "resolve working directory: %v\n", err)
		return 1
	}
	editor := web.NewEditor(web.EditorConfig{
		Sources: splicerun.TopologySourcesFor(root, workspaceTrusted(root)),
		Models:  editorModelChoices(root),
		Use: func(name string) (string, error) {
			topology, path, err := splicerun.ActivateLibraryPipeline(name)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Active pipeline: %s (%s). It applies from the next run.", topology.Name, path), nil
		},
	})
	id, err := editor.OpenDraft(ref, fresh)
	if err != nil {
		fmt.Fprintf(stderr, "pipeline edit: %v\n", err)
		return 1
	}
	ctx, stop := pipelineEditContext()
	defer stop()
	server := web.New(0)
	if err := editor.Attach(server); err != nil {
		fmt.Fprintf(stderr, "pipeline edit: %v\n", err)
		return 1
	}
	server.SetLanding(web.EditorSurface)
	if _, err := server.Start(ctx); err != nil {
		fmt.Fprintf(stderr, "pipeline edit: %v\n", err)
		return 1
	}
	defer server.Stop()
	link, err := server.BootstrapURLFor(web.EditorSurface, url.Values{"draft": {id}})
	if err != nil {
		fmt.Fprintf(stderr, "pipeline edit: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "Pipeline editor: %s\nOpen the link once in a browser on this computer. Press Ctrl-C to stop. The server stops after %s without use.\n", link, web.DefaultIdleTimeout)
	select {
	case <-ctx.Done():
	case <-server.Done():
	}
	fmt.Fprintln(stderr, "Pipeline editor stopped. The draft keeps the last saved state.")
	return 0
}

// editorModelChoices offers each configured provider profile with its model.
func editorModelChoices(root string) []web.ModelChoice {
	opts, err := config.WorkspaceResolveOptions(root, workspaceTrusted(root))
	if err != nil {
		return nil
	}
	resolved, err := config.Resolve(opts)
	if err != nil {
		return nil
	}
	var choices []web.ModelChoice
	for _, profile := range resolved.Providers {
		if strings.TrimSpace(profile.Name) != "" && strings.TrimSpace(profile.Model) != "" {
			choices = append(choices, web.ModelChoice{Provider: profile.Name, Model: profile.Model})
		}
	}
	return choices
}
