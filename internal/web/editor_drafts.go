package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Taf0711/splice/docs/examples"
	"github.com/Taf0711/splice/internal/config"
	"github.com/Taf0711/splice/internal/presentation"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// A draft is the editor's staging file: the topology document as the user
// left it, valid or not, and the node positions on the canvas. Positions are
// editor-only data. They never go into the topology document.
//
// Drafts live in <config>/splice/pipelines/.drafts/<id>.json, mode 0600. The
// server makes each id. A client id reaches the filesystem only after
// draftIDPattern accepts it, so no request can name another path.
type draft struct {
	ID string `json:"id"`
	// Base is the library entry that the draft edits, or "" for a new
	// pipeline or a copy.
	Base string `json:"base,omitempty"`
	// Source says where the draft came from, for the drafts list.
	Source string `json:"source"`
	// Origin keys the source for resume: "default", "project:<path>",
	// "library:<name>", or "example:<name>". A new pipeline has none.
	Origin    string          `json:"origin,omitempty"`
	Created   time.Time       `json:"created"`
	Updated   time.Time       `json:"updated"`
	Valid     bool            `json:"valid"`
	Doc       json.RawMessage `json:"doc"`
	Positions []position      `json:"positions"`
}

// position is the canvas position of the node at the same index in the
// document's node list.
type position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Limits of the canvas data. A client cannot grow a draft past them.
const (
	maxPositions = 4096
	maxCanvas    = 100000
)

// draftIDPattern is the only shape of a draft id: 128 random bits as hex.
var draftIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

var errNoDraft = errors.New("no such draft")

// draftsDir returns the drafts directory in the user config directory.
func draftsDir() (string, error) {
	base, err := config.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "splice", "pipelines", ".drafts"), nil
}

// draftPath returns the file of a draft id. It refuses every id that the
// server could not have made.
func draftPath(id string) (string, error) {
	if !draftIDPattern.MatchString(id) {
		return "", errNoDraft
	}
	dir, err := draftsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, id+".json"), nil
}

func loadDraft(id string) (draft, error) {
	path, err := draftPath(id)
	if err != nil {
		return draft{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return draft{}, errNoDraft
	}
	if err != nil {
		return draft{}, fmt.Errorf("read draft: %w", err)
	}
	var d draft
	if err := json.Unmarshal(data, &d); err != nil {
		return draft{}, fmt.Errorf("draft %s does not parse: %w", id, err)
	}
	if d.ID != id {
		return draft{}, fmt.Errorf("draft %s names another id", id)
	}
	return d, nil
}

// saveDraft writes the draft atomically with mode 0600.
func saveDraft(d draft) error {
	path, err := draftPath(d.ID)
	if err != nil {
		return err
	}
	// The library directory keeps the mode that import gives it; only the
	// drafts directory is private.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return fmt.Errorf("make the library directory: %w", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("make the drafts directory: %w", err)
	}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return splicerun.WriteFileAtomic(path, append(data, '\n'), 0o600)
}

func deleteDraft(id string) error {
	path, err := draftPath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
		return errNoDraft
	} else if err != nil {
		return fmt.Errorf("delete draft: %w", err)
	}
	return nil
}

// listDrafts returns the drafts, the latest change first. A file that does
// not load is left out.
func listDrafts() []draft {
	dir, err := draftsDir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var drafts []draft
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || entry.IsDir() || !draftIDPattern.MatchString(id) {
			continue
		}
		if d, err := loadDraft(id); err == nil {
			drafts = append(drafts, d)
		}
	}
	sort.SliceStable(drafts, func(i, j int) bool { return drafts[i].Updated.After(drafts[j].Updated) })
	return drafts
}

// draftName reads the topology name of a draft document, even an invalid one.
func draftName(doc json.RawMessage) string {
	var probe struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(doc, &probe)
	return probe.Name
}

// libraryNames lists the entries of the user library.
func libraryNames() []string {
	base, err := config.UserConfigDir()
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(base, "splice", "pipelines"))
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), ".json")
		if ok && !entry.IsDir() && schemas.ValidateTopologyName(name) == nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// Draft origins of a create request.
const (
	fromNew     = "new"
	fromActive  = "active"
	fromDefault = "default"
	fromLibrary = "library"
	fromExample = "example"
)

// exampleTopology loads one shipped example by its topology name.
func exampleTopology(name string) (*schemas.PipelineTopology, error) {
	for _, example := range exampleTopologies() {
		if example.Name == name {
			return example, nil
		}
	}
	return nil, fmt.Errorf("no example pipeline %q", name)
}

// exampleTopologies returns the shipped examples that load, in file order.
func exampleTopologies() []*schemas.PipelineTopology {
	paths, _ := fs.Glob(examples.Pipelines, "pipeline-*.json")
	var out []*schemas.PipelineTopology
	for _, path := range paths {
		data, err := examples.Pipelines.ReadFile(path)
		if err != nil {
			continue
		}
		if topology, err := splicerun.DecodeTopology(data); err == nil {
			out = append(out, &topology)
		}
	}
	return out
}

// newDraftName is the name of an empty draft.
const newDraftName = "new-pipeline"

// createDraft makes a draft from an origin. Unless fresh is set, a source
// that already has a draft resumes its latest draft, so a second edit does
// not start over. A new pipeline never resumes.
func createDraft(from, name string, sources splicerun.TopologySources, fresh bool) (draft, error) {
	now := time.Now().UTC()
	d := draft{Created: now, Updated: now}
	var topology *schemas.PipelineTopology
	resume := func() (draft, bool) {
		for _, existing := range listDrafts() {
			if !fresh && (existing.Origin == d.Origin || d.Base != "" && existing.Base == d.Base) {
				return existing, true
			}
		}
		return draft{}, false
	}
	switch from {
	case fromNew:
		d.Source = "new pipeline"
		topology = &schemas.PipelineTopology{Version: schemas.TopologySchemaVersion, Name: newDraftName, Nodes: []schemas.PipelineNode{}, Edges: []schemas.PipelineEdge{}}
	case fromDefault:
		d.Source, d.Origin = "copy of the embedded default", fromDefault
		if existing, ok := resume(); ok {
			return existing, nil
		}
		topology = splicerun.DefaultTopology()
	case fromExample:
		d.Source, d.Origin = "example "+name, fromExample+":"+name
		if existing, ok := resume(); ok {
			return existing, nil
		}
		example, err := exampleTopology(name)
		if err != nil {
			return draft{}, err
		}
		topology = example
	case fromLibrary:
		path, err := splicerun.LibraryEntryPath(name)
		if err != nil {
			return draft{}, err
		}
		d.Base, d.Source, d.Origin = name, "library entry "+name, fromLibrary+":"+name
		if existing, ok := resume(); ok {
			return existing, nil
		}
		loaded, err := splicerun.LoadTopologyFile(path, "")
		if err != nil {
			return draft{}, fmt.Errorf("library entry %s: %w", name, err)
		}
		topology = loaded
	case fromActive:
		resolved, source, _, err := splicerun.ResolveTopology(sources)
		if err != nil {
			return draft{}, fmt.Errorf("the active topology did not resolve: %w", err)
		}
		switch source {
		case splicerun.TopologySourceLibrary:
			return createDraft(fromLibrary, sources.ActiveName, sources, fresh)
		case splicerun.TopologySourceDefault:
			return createDraft(fromDefault, "", sources, fresh)
		}
		path := filepath.Join(sources.WorkspaceRoot, ".splice", "pipeline.json")
		d.Source, d.Origin = "copy of the project file "+path, "project:"+path
		if source != splicerun.TopologySourceProject {
			d.Source, d.Origin = "copy of the active topology ("+source+")", "active:"+source
		}
		if existing, ok := resume(); ok {
			return existing, nil
		}
		topology = resolved
	default:
		return draft{}, fmt.Errorf("unknown draft origin %q (want %s, %s, %s, %s, or %s)", from, fromNew, fromActive, fromDefault, fromLibrary, fromExample)
	}
	if topology.Name == splicerun.DefaultTopologyName {
		// The library cannot hold the reserved name, so a copy starts renamed.
		topology.Name = splicerun.DefaultTopologyName + "-copy"
	}
	doc, err := json.MarshalIndent(topology, "", "  ")
	if err != nil {
		return draft{}, err
	}
	id, err := newHex(16)
	if err != nil {
		return draft{}, err
	}
	d.ID, d.Doc, d.Positions = id, doc, initialPositions(topology)
	d.Valid = checkDoc(doc).Valid
	if err := saveDraft(d); err != nil {
		return draft{}, err
	}
	return d, nil
}

// initialPositions places the nodes where the pipeline viewer draws them, so
// the editor and the viewer start from one layout.
func initialPositions(topology *schemas.PipelineTopology) []position {
	positions := make([]position, len(topology.Nodes))
	if len(topology.Nodes) == 0 {
		return positions
	}
	graph, err := presentrun.BuildGraph(topology, presentation.State{})
	if err != nil {
		return gridPositions(len(topology.Nodes))
	}
	at := map[string]position{}
	for _, box := range layoutGraph(graph).Nodes {
		at[box.Frame.Name] = position{X: float64(box.X), Y: float64(box.Y)}
	}
	for i, node := range topology.Nodes {
		p, ok := at[node.Name]
		if !ok {
			return gridPositions(len(topology.Nodes))
		}
		positions[i] = p
	}
	return positions
}

// layoutDoc places the nodes of any document, valid or not, with the viewer
// layout. It is the editor's Auto-layout.
func layoutDoc(doc []byte) ([]position, error) {
	var loose schemas.PipelineTopology
	if err := json.Unmarshal(doc, &loose); err != nil {
		return nil, fmt.Errorf("the document does not parse: %w", err)
	}
	return initialPositions(&loose), nil
}

// gridPositions is the fallback: one column in declaration order.
func gridPositions(n int) []position {
	positions := make([]position, n)
	for i := range positions {
		positions[i] = position{X: layoutPad, Y: float64(layoutPad + i*(layoutNodeHeight+layoutGapY))}
	}
	return positions
}

// cleanPositions clamps the positions to the canvas.
func cleanPositions(positions []position) []position {
	out := make([]position, len(positions))
	for i, p := range positions {
		out[i] = position{X: min(max(p.X, 0), maxCanvas), Y: min(max(p.Y, 0), maxCanvas)}
	}
	return out
}

// isJSONObject reports whether raw is one JSON object.
func isJSONObject(raw json.RawMessage) bool {
	return bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{"))
}
