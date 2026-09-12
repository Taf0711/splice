package splice

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/splice/schemas"
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
	// UserConfigDir is the base for the user library. Empty uses the default.
	UserConfigDir string
}

// TopologySourcesFor builds the sources a run can resolve from the agent
// options and the per-user config. A missing or unreadable config.json leaves
// ActiveName empty; the run then falls back to the embedded default.
func TopologySourcesFor(workspaceRoot string, trusted bool) TopologySources {
	sources := TopologySources{WorkspaceRoot: workspaceRoot, Trusted: trusted}
	base, err := config.UserConfigDir()
	if err != nil {
		return sources
	}
	sources.UserConfigDir = base
	if name, err := activePipelineName(filepath.Join(base, "splice", "config.json")); err == nil {
		sources.ActiveName = name
	}
	return sources
}

// ResolveTopology returns the active topology and its source label. Precedence:
// a trusted project file, then the --pipeline file, then the active user
// library entry, then the embedded default.
//
// A present-but-invalid file fails loud instead of silently falling back, so a
// typo in an executable config cannot quietly change which pipeline runs. An
// untrusted project file is ignored and reported as a warning.
func ResolveTopology(sources TopologySources) (*schemas.PipelineTopology, string, []string, error) {
	var warnings []string
	libraryDir, dirErr := sources.libraryDir()
	if dirErr != nil {
		// An unresolvable per-user config directory is not fatal: the run
		// falls back to the project, flag, and embedded default.
		libraryDir = ""
		warnings = append(warnings, "could not resolve the user config directory: "+dirErr.Error())
	}
	projectPath := ""
	if strings.TrimSpace(sources.WorkspaceRoot) != "" {
		projectPath = filepath.Join(sources.WorkspaceRoot, ".splice", "pipeline.json")
	}
	if projectPath != "" {
		topology, present, err := loadTopologyIfPresent(projectPath)
		switch {
		case err != nil:
			return nil, "", nil, fmt.Errorf("project topology %s: %w", projectPath, err)
		case present && sources.Trusted:
			return topology, TopologySourceProject, warnings, nil
		case present:
			warnings = append(warnings, "ignored untrusted project topology "+projectPath)
		}
	}
	if path := sources.flagTopologyPath(libraryDir); path != "" {
		topology, present, err := loadTopologyIfPresent(path)
		if err != nil {
			return nil, "", nil, fmt.Errorf("--pipeline topology %s: %w", path, err)
		}
		if present {
			return topology, TopologySourceFlag, warnings, nil
		}
		warnings = append(warnings, "--pipeline topology "+path+" was not found")
	}
	if path := libraryPath(libraryDir, sources.ActiveName); path != "" {
		topology, present, err := loadTopologyIfPresent(path)
		if err != nil {
			return nil, "", nil, fmt.Errorf("library topology %s: %w", path, err)
		}
		if present {
			return topology, TopologySourceLibrary, warnings, nil
		}
		warnings = append(warnings, "active pipeline "+sources.ActiveName+" was not found in "+libraryDir)
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
	if strings.ContainsRune(name, filepath.Separator) || strings.HasSuffix(name, ".json") {
		return name
	}
	return libraryPath(libraryDir, name)
}

// LoadNamedTopology loads one topology by --pipeline reference (a library name
// or a file path) and returns it with the resolved path. It fails loud when the
// reference does not resolve or the file does not parse and validate.
func LoadNamedTopology(reference string) (*schemas.PipelineTopology, string, error) {
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
	topology, present, err := loadTopologyIfPresent(path)
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
// file.
func loadTopologyIfPresent(path string) (*schemas.PipelineTopology, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read: %w", err)
	}
	var topology schemas.PipelineTopology
	if err := json.Unmarshal(data, &topology); err != nil {
		return nil, false, fmt.Errorf("parse: %w", err)
	}
	if err := topology.Validate(); err != nil {
		return nil, false, err
	}
	return &topology, true, nil
}

// activePipelineName reads config.json's active_pipeline pointer. A missing
// config file is not an error.
func activePipelineName(configPath string) (string, error) {
	data, err := os.ReadFile(configPath)
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
