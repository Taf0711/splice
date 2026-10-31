package splice

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Taf0711/splice/internal/agent"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/projectfile"
	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/splice/stages"
	"github.com/Taf0711/splice/internal/version"
)

// Topology source labels. They are stable so a caller can report where the
// active pipeline came from.
const (
	TopologySourceDefault = "embedded default"
	TopologySourceProject = "project .splice/pipeline.json"
	TopologySourceFlag    = "--pipeline"
	TopologySourceLibrary = "user library"
)

// TopologySources names the candidate topology layers. The project file is
// executable config and is honored only when Trusted is true; the flag and the
// user library are user-installed consent.
type TopologySources struct {
	// WorkspaceRoot scopes the project file (.splice/pipeline.json).
	WorkspaceRoot string
	// Trusted gates the project file.
	Trusted bool
	// FlagPath is a resolved --pipeline file path, or empty.
	FlagPath string
	// FlagName is the raw --pipeline value: a library name or a file path. It is
	// resolved against the library before FlagPath is considered.
	FlagName string
	// ActiveName is config.json's active_pipeline library name, or empty.
	ActiveName string
	// ActiveNameErr is set when config.json exists but cannot be read or
	// parsed. The active pipeline is then unknown, and a resolve that would
	// consult it fails instead of running the embedded default.
	ActiveNameErr error
	// UserConfigDir is the base for the user library. Empty uses the default.
	UserConfigDir string
}

// TopologySourcesFor builds the sources a run can resolve from the agent
// options and the per-user config. A missing config.json leaves ActiveName
// empty, and the run uses the embedded default. A config.json that exists
// but does not parse sets ActiveNameErr, so the run fails loud instead of
// silently dropping active_pipeline.
func TopologySourcesFor(workspaceRoot string, trusted bool) TopologySources {
	sources := TopologySources{WorkspaceRoot: workspaceRoot, Trusted: trusted}
	base, err := config.UserConfigDir()
	if err != nil {
		return sources
	}
	sources.UserConfigDir = base
	name, err := activePipelineName(filepath.Join(base, "splice", "config.json"))
	if err != nil {
		sources.ActiveNameErr = err
		return sources
	}
	sources.ActiveName = name
	return sources
}

// topologyRoot returns the directory whose project topology a run loads. It is
// TrustRoot when set, so the file that runs commands comes from the directory
// whose trust decision was made. Otherwise it is ProjectRoot, then Cwd.
func topologyRoot(options agent.Options) string {
	if options.TrustRoot != "" {
		return options.TrustRoot
	}
	if options.ProjectRoot != "" {
		return options.ProjectRoot
	}
	return options.Cwd
}

// resolveRunTopology resolves the active topology for a run from its options.
func resolveRunTopology(options agent.Options) (*schemas.PipelineTopology, string, []string, error) {
	sources := TopologySourcesFor(topologyRoot(options), options.TrustedWorkspace)
	sources.FlagName = options.Pipeline
	return ResolveTopology(sources)
}

// ResolveTopology returns the active topology and its source label. Precedence:
// the --pipeline file or library entry, then a trusted project file, then the
// active user library entry, then the embedded default. A --pipeline value
// overrides a trusted project file, and the run reports that override.
//
// A requested topology that does not resolve fails loud, and so does a
// present-but-invalid file, so a typo cannot quietly change which pipeline
// runs. An untrusted project file is ignored and reported as a warning.
func ResolveTopology(sources TopologySources) (*schemas.PipelineTopology, string, []string, error) {
	var warnings []string
	libraryDir, dirErr := sources.libraryDir()
	if dirErr != nil {
		// A library reference cannot resolve without the directory, so it is
		// an error. Otherwise the project and embedded default still apply.
		if sources.needsLibrary() {
			return nil, "", nil, fmt.Errorf("resolve the user library for a requested topology: %w", dirErr)
		}
		libraryDir = ""
		warnings = append(warnings, "could not resolve the user config directory: "+dirErr.Error())
	}
	projectPath := ""
	if strings.TrimSpace(sources.WorkspaceRoot) != "" {
		projectPath = filepath.Join(sources.WorkspaceRoot, ".splice", "pipeline.json")
	}
	// Trust is resolved before the project file is read. An untrusted project
	// file is ignored, so a malformed untrusted file cannot fail the run.
	projectPresent := projectPath != "" && filePresent(projectPath)
	if projectPresent && !sources.Trusted {
		warnings = append(warnings, "ignored untrusted project topology "+projectPath)
	}
	// The name "default" selects the embedded topology, never a library entry,
	// so --pipeline default and active_pipeline default always resolve the same.
	if isEmbeddedDefaultRef(sources.FlagName) {
		if sources.Trusted && projectPresent {
			warnings = append(warnings, "--pipeline default overrides the trusted project topology "+projectPath)
		}
		return defaultTopology(), TopologySourceDefault, warnings, nil
	}
	if path := sources.flagTopologyPath(libraryDir); path != "" {
		topology, present, err := loadTopologyIfPresent(path, "")
		if err != nil {
			return nil, "", nil, fmt.Errorf("--pipeline topology %s: %w", path, err)
		}
		if !present {
			return nil, "", nil, fmt.Errorf("--pipeline %s: topology not found at %s", sources.flagReference(path), path)
		}
		if sources.Trusted && projectPresent && filepath.Clean(path) != filepath.Clean(projectPath) {
			warnings = append(warnings, "--pipeline "+path+" overrides the trusted project topology "+projectPath)
		}
		return topology, TopologySourceFlag, warnings, nil
	}
	if sources.Trusted && projectPresent {
		topology, present, err := loadTopologyIfPresent(projectPath, sources.WorkspaceRoot)
		if err != nil {
			return nil, "", nil, fmt.Errorf("project topology %s: %w", projectPath, err)
		}
		if present {
			return topology, TopologySourceProject, warnings, nil
		}
	}
	if sources.ActiveNameErr != nil {
		// The pointer to the active library entry is unreadable; running the
		// default would silently change which pipeline runs.
		return nil, "", nil, fmt.Errorf("the active pipeline is unknown: %w. Fix the file or remove its active_pipeline key", sources.ActiveNameErr)
	}
	if isEmbeddedDefaultRef(sources.ActiveName) {
		return defaultTopology(), TopologySourceDefault, warnings, nil
	}
	if name := strings.TrimSpace(sources.ActiveName); name != "" {
		path := libraryPath(libraryDir, name)
		topology, present, err := loadTopologyIfPresent(path, "")
		if err != nil {
			return nil, "", nil, fmt.Errorf("library topology %s: %w", path, err)
		}
		if !present {
			return nil, "", nil, fmt.Errorf("config.json active_pipeline %s: no library entry at %s", name, path)
		}
		return topology, TopologySourceLibrary, warnings, nil
	}
	return defaultTopology(), TopologySourceDefault, warnings, nil
}

// Sources returns the candidate file paths in precedence order. It is exported
// for callers that report or edit the topology locations.
func (s TopologySources) Sources() (project, flag, library string, err error) {
	libraryDir, err := s.libraryDir()
	if err != nil {
		return "", "", "", err
	}
	if strings.TrimSpace(s.WorkspaceRoot) != "" {
		project = filepath.Join(s.WorkspaceRoot, ".splice", "pipeline.json")
	}
	flag = s.flagTopologyPath(libraryDir)
	library = libraryPath(libraryDir, s.ActiveName)
	return project, flag, library, nil
}

// flagTopologyPath resolves the --pipeline value: a value with a path
// separator or a .json suffix is a file path, anything else names a library
// entry. FlagName wins over an explicit FlagPath.
func (s TopologySources) flagTopologyPath(libraryDir string) string {
	name := strings.TrimSpace(s.FlagName)
	if name == "" {
		return strings.TrimSpace(s.FlagPath)
	}
	if isTopologyFileRef(name) {
		return name
	}
	return libraryPath(libraryDir, name)
}

// isTopologyFileRef reports whether a --pipeline value names a file path rather
// than a library entry.
func isTopologyFileRef(ref string) bool {
	return strings.ContainsRune(ref, filepath.Separator) || strings.HasSuffix(ref, ".json")
}

// needsLibrary reports whether resolution reads the user library: a library
// name in --pipeline, or an active_pipeline entry.
func (s TopologySources) needsLibrary() bool {
	name := strings.TrimSpace(s.FlagName)
	active := strings.TrimSpace(s.ActiveName)
	return (active != "" && !isEmbeddedDefaultRef(active)) ||
		(name != "" && !isTopologyFileRef(name) && !isEmbeddedDefaultRef(name))
}

// isEmbeddedDefaultRef reports whether a pipeline reference names the embedded
// default topology. The name is reserved: a library entry cannot take it.
func isEmbeddedDefaultRef(ref string) bool {
	return strings.TrimSpace(ref) == DefaultTopologyName
}

// flagReference returns the --pipeline value for messages, or path when the
// flag was given as a file path.
func (s TopologySources) flagReference(path string) string {
	if name := strings.TrimSpace(s.FlagName); name != "" {
		return name
	}
	return path
}

// LoadNamedTopology loads one topology by --pipeline reference (a library name
// or a file path) and returns it with the resolved path. It fails loud when the
// reference does not resolve or the file does not parse and validate.
func LoadNamedTopology(reference string) (*schemas.PipelineTopology, string, error) {
	if isEmbeddedDefaultRef(reference) {
		return defaultTopology(), TopologySourceDefault, nil
	}
	// A library name becomes a file name, so it must pass the name rule.
	if name := strings.TrimSpace(reference); name != "" && !isTopologyFileRef(name) {
		if err := schemas.ValidateTopologyName(name); err != nil {
			return nil, "", err
		}
	}
	base, err := config.UserConfigDir()
	if err != nil {
		return nil, "", err
	}
	sources := TopologySources{FlagName: reference, UserConfigDir: base}
	libraryDir, err := sources.libraryDir()
	if err != nil {
		return nil, "", err
	}
	path := sources.flagTopologyPath(libraryDir)
	if path == "" {
		return nil, "", fmt.Errorf("pipeline reference is empty")
	}
	topology, present, err := loadTopologyIfPresent(path, "")
	if err != nil {
		return nil, path, err
	}
	if !present {
		return nil, path, fmt.Errorf("topology %s not found", path)
	}
	return topology, path, nil
}

func (s TopologySources) libraryDir() (string, error) {
	base := strings.TrimSpace(s.UserConfigDir)
	if base == "" {
		var err error
		base, err = config.UserConfigDir()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(base, "splice", "pipelines"), nil
}

func libraryPath(dir, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	return filepath.Join(dir, name+".json")
}

// loadTopologyIfPresent reads, parses, and validates one topology file. A
// missing file is (nil, false, nil); any other failure is an error naming the
// file. Pass the workspace root for a project topology so it cannot resolve
// outside the workspace; pass "" for operator-owned files.
func loadTopologyIfPresent(path, root string) (*schemas.PipelineTopology, bool, error) {
	data, err := projectfile.Read(path, root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read: %w", err)
	}
	topology, err := DecodeTopology(data)
	if err != nil {
		return nil, false, err
	}
	return &topology, true, nil
}

// DecodeTopology parses and validates one topology document. It reads only
// the name and splice_min_version first and checks the minimum against this
// build, so a file written for a newer Splice names the required version
// instead of an unknown field. Then it decodes strictly: an unknown field is an
// error that names the field, so a typo such as "modle" cannot silently drop a
// setting. Data after the topology object is also an error. Import and the run
// loader both use this check.
func DecodeTopology(data []byte) (schemas.PipelineTopology, error) {
	var probe struct {
		Name      string `json:"name"`
		MinSplice string `json:"splice_min_version"`
	}
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&probe); err != nil {
		return schemas.PipelineTopology{}, fmt.Errorf("parse: %w", err)
	}
	if err := (schemas.PipelineTopology{Name: probe.Name, MinSplice: probe.MinSplice}).ValidateMinSplice(version.Version); err != nil {
		return schemas.PipelineTopology{}, err
	}
	var topology schemas.PipelineTopology
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&topology); err != nil {
		return schemas.PipelineTopology{}, fmt.Errorf("parse: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return schemas.PipelineTopology{}, fmt.Errorf("parse: unexpected data after the topology object")
	}
	if err := topology.Validate(); err != nil {
		return schemas.PipelineTopology{}, err
	}
	if err := ValidateTopologyTiers(&topology); err != nil {
		return schemas.PipelineTopology{}, err
	}
	return topology, nil
}

// LoadTopologyFile reads and validates one topology file at path. Pass the
// workspace root for a project file so it cannot resolve outside the workspace.
// A missing file is an error that matches fs.ErrNotExist.
func LoadTopologyFile(path, root string) (*schemas.PipelineTopology, error) {
	topology, present, err := loadTopologyIfPresent(path, root)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, fmt.Errorf("read %s: %w", path, fs.ErrNotExist)
	}
	return topology, nil
}

// filePresent reports whether path names an existing regular file. It only
// decides whether to warn about an ignored untrusted topology, so a stat
// error is treated as absent.
func filePresent(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// activePipelineName reads config.json's active_pipeline pointer. A missing
// config file is not an error.
func activePipelineName(configPath string) (string, error) {
	data, err := projectfile.Read(configPath, "")
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read %s: %w", configPath, err)
	}
	var parsed struct {
		ActivePipeline string `json:"active_pipeline"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("parse %s: %w", configPath, err)
	}
	return strings.TrimSpace(parsed.ActivePipeline), nil
}

// ActivateLibraryPipeline makes a library entry the active pipeline. It is the
// one path behind `splice pipeline use` and the TUI `/pipeline use`. The name
// is checked and the topology loaded before config.json is written, so a bad
// reference leaves the config unchanged. The run reads the active name when it
// starts, so the change applies from the next run.
func ActivateLibraryPipeline(name string) (*schemas.PipelineTopology, string, error) {
	name = strings.TrimSpace(name)
	// active_pipeline holds a library name, never a path. A file must be
	// imported into the library first.
	if err := schemas.ValidateTopologyName(name); err != nil {
		return nil, "", fmt.Errorf("%w. To use a topology file, run: splice pipeline import <path>", err)
	}
	topology, path, err := LoadNamedTopology(name)
	if err != nil {
		return nil, "", err
	}
	if err := setActiveLibraryName(name); err != nil {
		return nil, "", err
	}
	return topology, path, nil
}

// setActiveLibraryName writes config.json's active_pipeline while preserving
// every other key, so a topology selection never drops unrelated configuration.
func setActiveLibraryName(name string) error {
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
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return WriteFileAtomic(path, append(out, '\n'), mode)
}

// LibraryEntryPath returns the user-library path for a pipeline name. It
// refuses a name that fails the name rule, so a path can never leave the
// library directory. It refuses the reserved name of the embedded topology.
// Import and the browser editor both write through it.
func LibraryEntryPath(name string) (string, error) {
	if err := schemas.ValidateTopologyName(name); err != nil {
		return "", err
	}
	if name == DefaultTopologyName {
		return "", fmt.Errorf("pipeline name %q is reserved for the embedded topology; rename the topology", name)
	}
	base, err := config.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "splice", "pipelines", name+".json"), nil
}

// WriteFileAtomic writes data to a temporary file in the target directory and
// renames it over path, so a concurrent reader sees the old file or the new
// one, never a partial write. The file gets mode. The directory must exist.
func WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return fmt.Errorf("set the mode of %s: %w", path, err)
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return fmt.Errorf("sync %s: %w", path, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// AgentNodeDisclosure is what a reviewer must see about one agent node before
// it runs: its tools and what they can reach, the permission it runs under,
// its turn and token caps, and a bounded preview of its prompt. Each text is
// sanitized. The terminal trust card and the web viewer both show it.
type AgentNodeDisclosure struct {
	Name  string
	Tools []string
	// Marks name the reach of the tools beyond reading files, such as "can
	// run commands" or "network".
	Marks []string
	// Permission is the node's own mode, or "run mode" when it sets none.
	Permission string
	// PermissionRule says in words how the run mode and the node's mode
	// combine (see effectiveAgentPermission).
	PermissionRule string
	MaxTurns       int
	Budget         string
	// PromptPreview is the quoted start of the prompt, with the count of the
	// runes left out.
	PromptPreview string
}

// DescribeAgentNode returns the disclosure of one agent node.
func DescribeAgentNode(node schemas.PipelineNode) AgentNodeDisclosure {
	permission := "run mode"
	if node.Permission != "" {
		permission = node.Permission
	}
	turns := node.MaxTurns
	if turns == 0 {
		turns = schemas.AgentDefaultMaxTurns
	}
	tools := make([]string, len(node.Tools))
	for i, tool := range node.Tools {
		tools[i] = sanitizeNoticeLine(tool)
	}
	return AgentNodeDisclosure{
		Name:           sanitizeNoticeLine(node.Name),
		Tools:          tools,
		Marks:          agentNodeCapabilityMarks(node.Tools),
		Permission:     permission,
		PermissionRule: agentPermissionRule(node.Permission),
		MaxTurns:       turns,
		Budget:         agentNodeBudgetText(node),
		PromptPreview:  agentPromptPreview(node.Prompt),
	}
}

// agentPermissionRule states the effective permission of an agent node in
// words, the rule of effectiveAgentPermission: a node can only tighten the
// run, and an agent node never runs unsandboxed.
func agentPermissionRule(nodePermission string) string {
	if nodePermission == schemas.AgentPermissionAsk {
		return "ask, whatever the run mode; " + agentAskText
	}
	return "the run mode, and auto when the run is unsafe; ask when the run mode is ask; " + agentAskText
}

// agentAskText says what ask means for an agent node. The loop prompts for
// every call to a tool that is not read-only, also when the sandbox or a saved
// grant would allow the call on its own. Read-only tools do not prompt.
const agentAskText = "ask prompts for every write, command, and network call"

// agentPermissionText names a permission for a disclosure line. Ask carries
// what it prompts for.
func agentPermissionText(permission string) string {
	if permission == schemas.AgentPermissionAsk {
		return "ask (" + strings.TrimPrefix(agentAskText, "ask ") + ")"
	}
	return permission
}

// AgentNodeLine describes one agent node: its tools and the permission it runs
// under. The permission names the node's own mode, or the run mode when the
// node sets none. The run mode can only tighten an "auto" node, never loosen it.
func AgentNodeLine(node schemas.PipelineNode) string {
	d := DescribeAgentNode(node)
	line := fmt.Sprintf("%s: tools %s; permission %s; up to %d turns; %s; prompt %s", node.Name, strings.Join(node.Tools, ", "), agentPermissionText(d.Permission), d.MaxTurns, d.Budget, d.PromptPreview)
	if len(d.Marks) > 0 {
		line += "; " + strings.Join(d.Marks, ", ")
	}
	// Every disclosure line passes the notice sanitizer, so no control
	// character from a topology file reaches the terminal.
	return sanitizeNoticeLine(line)
}

// AgentNodes returns the disclosure of every agent node, in file order.
func AgentNodes(topology *schemas.PipelineTopology) []AgentNodeDisclosure {
	var nodes []AgentNodeDisclosure
	for _, node := range topology.Nodes {
		if node.Type == schemas.NodeTypeAgent {
			nodes = append(nodes, DescribeAgentNode(node))
		}
	}
	return nodes
}

// AgentBudgetSums returns the input and output token caps of all agent nodes.
func AgentBudgetSums(topology *schemas.PipelineTopology) (input, output int) {
	return agentBudgetSums(topology)
}

// AgentBudgetSumText states the token caps of all agent nodes, which the run
// adds on top of the tier envelope.
func AgentBudgetSumText(topology *schemas.PipelineTopology) string {
	input, output := agentBudgetSums(topology)
	return fmt.Sprintf("agent nodes: input %d, output %d tokens, added on top of the tier envelope", input, output)
}

// agentPromptPreviewRunes bounds the agent prompt shown in a disclosure. The
// rest of the prompt is counted, not shown.
const agentPromptPreviewRunes = 200

// agentPromptPreview returns the first runes of an agent prompt, quoted and
// sanitized, with the count of the runes left out. A reviewer sees what the node
// is told to do, and a long or hostile prompt cannot flood or rewrite the screen.
func agentPromptPreview(prompt string) string {
	clean := sanitizeNoticeLine(prompt)
	runes := []rune(clean)
	if len(runes) <= agentPromptPreviewRunes {
		return fmt.Sprintf("%q", clean)
	}
	return fmt.Sprintf("%q +%d more", string(runes[:agentPromptPreviewRunes]), len(runes)-agentPromptPreviewRunes)
}

// agentNodeBudgetText states an agent node's explicit input and output cap. The
// cap is added on top of the tier envelope.
func agentNodeBudgetText(node schemas.PipelineNode) string {
	if node.Budget == nil {
		return "no budget"
	}
	return fmt.Sprintf("input %d, output %d tokens", node.Budget.InputMax, node.Budget.OutputMax)
}

// agentNodeCapabilityMarks names the reach of an agent node's tools, so the
// disclosure says what the node can do beyond reading files. A shell tool can
// run commands. The web fetch tool reaches the network.
func agentNodeCapabilityMarks(allowlist []string) []string {
	var marks []string
	for _, name := range allowlist {
		switch name {
		case "bash", "exec_command":
			if !containsMark(marks, "can run commands") {
				marks = append(marks, "can run commands")
			}
		case "web_fetch":
			if !containsMark(marks, "network") {
				marks = append(marks, "network")
			}
		}
	}
	return marks
}

func containsMark(marks []string, want string) bool {
	for _, mark := range marks {
		if mark == want {
			return true
		}
	}
	return false
}

// AgentNodeLines describes the agent nodes of a topology. An agent node runs a
// model with tools, so it is disclosed with the same care as a command node. It
// returns nothing when the topology has no agent node.
func AgentNodeLines(topology *schemas.PipelineTopology) []string {
	var lines []string
	for _, node := range topology.Nodes {
		if node.Type != schemas.NodeTypeAgent {
			continue
		}
		lines = append(lines, "  agent node "+AgentNodeLine(node))
	}
	if len(lines) == 0 {
		return nil
	}
	return append(lines,
		"  "+AgentNodeCountText(len(lines)),
		"  + "+AgentBudgetSumText(topology))
}

// AgentNodeCountText says how many agent nodes run a model with tools.
func AgentNodeCountText(n int) string {
	if n == 1 {
		return "1 agent node runs a model with its listed tools when this topology is active"
	}
	return fmt.Sprintf("%d agent nodes run a model with their listed tools when this topology is active", n)
}

// agentBudgetSums returns the input and output caps of every agent node.
func agentBudgetSums(topology *schemas.PipelineTopology) (input, output int) {
	for _, node := range topology.Nodes {
		if node.Type == schemas.NodeTypeAgent && node.Budget != nil {
			input += node.Budget.InputMax
			output += node.Budget.OutputMax
		}
	}
	return input, output
}

// CommandNode is one executable command node of a topology.
type CommandNode struct {
	Name string
	// Line is the exact command line.
	Line string
}

// CommandNodes returns the command nodes of a topology in declaration order.
func CommandNodes(topology *schemas.PipelineTopology) []CommandNode {
	var nodes []CommandNode
	for _, node := range topology.Nodes {
		if node.Type == schemas.NodeTypeCommand {
			nodes = append(nodes, CommandNode{Name: node.Name, Line: stages.CommandLine(node.Command)})
		}
	}
	return nodes
}

// CommandNodeCountText says how many command nodes run shell commands.
func CommandNodeCountText(n int) string {
	if n == 1 {
		return "1 command node runs shell commands when this topology is active"
	}
	return fmt.Sprintf("%d command nodes run shell commands when this topology is active", n)
}

// CommandNodeLines describes the executable command nodes of a topology, so a
// command is visible before it is installed or activated. It returns nothing
// when the topology has no command node.
func CommandNodeLines(topology *schemas.PipelineTopology) []string {
	nodes := CommandNodes(topology)
	if len(nodes) == 0 {
		return nil
	}
	lines := make([]string, 0, len(nodes)+1)
	for _, node := range nodes {
		// Every disclosure line passes the notice sanitizer, so no control
		// character from a topology file reaches the terminal.
		lines = append(lines, sanitizeNoticeLine(fmt.Sprintf("  command node %s: %s", node.Name, node.Line)))
	}
	return append(lines, "  "+CommandNodeCountText(len(nodes)))
}
