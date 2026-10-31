package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Taf0711/splice/internal/modelregistry"
	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/presentrun"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// EditorSurface is the surface name of the topology editor.
const EditorSurface = "editor"

// maxEditorBody caps one editor request body. It is the import cap.
const maxEditorBody = 1 << 20

// ModelChoice is one provider and model that the node panel offers.
type ModelChoice struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// EditorConfig is what the editor needs from the program that serves it.
type EditorConfig struct {
	// Sources resolve the active topology, which the editor can copy.
	Sources splicerun.TopologySources
	// Models are the provider and model options of the node panel: the
	// stage-model options that the TUI offers.
	Models []ModelChoice
	// Use makes a library entry the active pipeline, as `/pipeline use` does.
	// It returns the words to show. Nil hides "Use it now".
	Use func(name string) (string, error)
}

// Editor is the browser topology editor. It edits drafts in the user config
// directory and publishes them to the user library. It never writes a
// project file (.splice/pipeline.json): a project gets a topology through
// `splice pipeline export`. A running run keeps the topology that it started
// with, so an edit or a publish never changes it.
//
// Every write goes through the surface Mutate path, so it needs the session
// cookie, a matching Origin, and the action token.
type Editor struct {
	mu  sync.Mutex // serializes the draft and library writes
	cfg EditorConfig
}

// NewEditor returns an editor with the given configuration.
func NewEditor(cfg EditorConfig) *Editor {
	return &Editor{cfg: cfg}
}

// Configure replaces the configuration.
func (e *Editor) Configure(cfg EditorConfig) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cfg = cfg
}

func (e *Editor) config() EditorConfig {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg
}

// Attach registers the editor as the editor surface of server.
func (e *Editor) Attach(server *Server) error {
	page := func(w http.ResponseWriter, r *http.Request, action string) {
		e.servePage(w, r, action, server.hasSurface(PipelineSurface))
	}
	return server.Register(EditorSurface, Surface{Page: page, Sub: e.serveSub, Mutate: http.HandlerFunc(e.serveMutate)})
}

// OpenDraft makes a draft for an entry point and returns its id. ref is a
// library entry name, "default" for a copy of the embedded default, or ""
// for the active topology. A source that has a draft resumes its latest
// draft, unless fresh is set.
func (e *Editor) OpenDraft(ref string, fresh bool) (string, error) {
	from := fromLibrary
	switch strings.TrimSpace(ref) {
	case "":
		from = fromActive
	case splicerun.DefaultTopologyName:
		from = fromDefault
	}
	cfg := e.config()
	e.mu.Lock()
	defer e.mu.Unlock()
	d, err := createDraft(from, strings.TrimSpace(ref), cfg.Sources, fresh)
	if err != nil {
		return "", err
	}
	return d.ID, nil
}

// ParseEditArgs reads the arguments of `/pipeline edit` and `splice pipeline
// edit`: an optional --new, and at most one library name or "default".
func ParseEditArgs(args []string) (ref string, fresh bool, err error) {
	for _, arg := range args {
		switch {
		case arg == "--new":
			fresh = true
		case strings.HasPrefix(arg, "-"):
			return "", false, fmt.Errorf("unknown flag %q", arg)
		case ref != "":
			return "", false, fmt.Errorf("unexpected argument %q", arg)
		default:
			ref = arg
		}
	}
	return ref, fresh, nil
}

var editorTemplates = template.Must(template.ParseFS(assetFS, "assets/editor.html"))

// editorHome is the data of the drafts page.
type editorHome struct {
	Action       string
	Drafts       []draftRow
	Library      []string
	LibraryDir   string
	Active       string
	ActiveSource string
	ActiveError  string
	// ActiveIsLibrary is true when the active topology is a library entry:
	// its draft edits the entry instead of a copy.
	ActiveIsLibrary bool
	Examples        []exampleRow
	FromActive      bool
	// Viewer links the pipeline viewer when this server serves it.
	Viewer bool
}

type exampleRow struct{ Name, About string }

type draftRow struct {
	ID, Name, Source, Updated string
	Valid                     bool
}

// editorPage is the data of the editor page. Boot is the JSON state that the
// script reads from a data attribute, so the page needs no inline script.
type editorPage struct {
	Action      string
	Name        string
	Source      string
	Boot        string
	Types       []nodeTypeView
	Tools       []toolView
	Permissions []permissionView
	Efforts     []string
	Tiers       []string
	Providers   []string
}

type nodeTypeView struct {
	Name      string `json:"name"`
	Builtin   bool   `json:"builtin"`
	ModelFree bool   `json:"model_free"`
	About     string `json:"about"`
	// Kind is the subtitle word of the viewer for a node named as its type.
	Kind string `json:"kind"`
}

// typeAbout is the one-line description of each node type in the palette.
var typeAbout = map[string]string{
	"code_writer":           "Writes and changes the code.",
	"test_generator":        "Writes tests for the change.",
	"static_analyzer":       "Runs the static analyzers. No model.",
	"security_auditor":      "Checks the change for security problems. No model.",
	"test_runner":           "Runs the tests. No model.",
	"acceptance_verifier":   "Checks the acceptance criteria. No model.",
	schemas.NodeTypePrompt:  "One model call with your prompt.",
	schemas.NodeTypeCommand: "Runs one command, exactly as listed. No model.",
	schemas.NodeTypeAgent:   "A model with a tool allowlist, in a bounded loop.",
}

type toolView struct {
	Name  string `json:"name"`
	Marks string `json:"marks"`
}

type permissionView struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Rule  string `json:"rule"`
}

// editorBoot is the state that the script starts from.
type editorBoot struct {
	ID          string           `json:"id"`
	Doc         json.RawMessage  `json:"doc"`
	Positions   []position       `json:"positions"`
	Check       editorCheck      `json:"check"`
	Types       []nodeTypeView   `json:"types"`
	Permissions []permissionView `json:"permissions"`
	Models      []ModelChoice    `json:"models"`
	AgentMin    string           `json:"agent_min"`
	CanUse      bool             `json:"can_use"`
	Saved       string           `json:"saved"`
}

func (e *Editor) servePage(w http.ResponseWriter, r *http.Request, action string, viewer bool) {
	if id := r.URL.Query().Get("draft"); id != "" {
		e.serveEditorPage(w, id, action)
		return
	}
	cfg := e.config()
	home := editorHome{Action: action, Library: libraryNames(), FromActive: r.URL.Query().Get("from") == fromActive, Viewer: viewer}
	for _, d := range listDrafts() {
		home.Drafts = append(home.Drafts, draftRow{ID: d.ID, Name: draftName(d.Doc), Source: d.Source, Updated: d.Updated.Local().Format("2006-01-02 15:04"), Valid: d.Valid})
	}
	if dir, err := draftsDir(); err == nil {
		home.LibraryDir = filepath.Dir(dir)
	}
	for _, example := range exampleTopologies() {
		home.Examples = append(home.Examples, exampleRow{Name: example.Name, About: example.Description})
	}
	if topology, source, _, err := splicerun.ResolveTopology(cfg.Sources); err != nil {
		home.ActiveError = err.Error()
	} else {
		home.Active, home.ActiveSource = topology.Name, source
		home.ActiveIsLibrary = source == splicerun.TopologySourceLibrary
	}
	renderEditor(w, "home", home)
}

func (e *Editor) serveEditorPage(w http.ResponseWriter, id, action string) {
	d, err := loadDraft(id)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		renderEditor(w, "missing", editorHome{Action: action})
		return
	}
	cfg := e.config()
	page := editorPage{Action: action, Name: draftName(d.Doc), Source: d.Source, Tools: editorTools(), Permissions: editorPermissions(), Tiers: editorTiers()}
	page.Types = editorTypes()
	for _, level := range modelregistry.ReasoningEffortLevels() {
		page.Efforts = append(page.Efforts, string(level))
	}
	seen := map[string]bool{}
	for _, choice := range cfg.Models {
		if !seen[choice.Provider] {
			seen[choice.Provider] = true
			page.Providers = append(page.Providers, choice.Provider)
		}
	}
	boot := editorBoot{
		ID: d.ID, Doc: d.Doc, Positions: d.Positions, Check: checkDoc(d.Doc),
		Types: page.Types, Permissions: page.Permissions, Models: cfg.Models,
		AgentMin: schemas.AgentNodeMinSplice, CanUse: cfg.Use != nil,
		Saved: d.Updated.Local().Format("15:04:05"),
	}
	if boot.Models == nil {
		boot.Models = []ModelChoice{}
	}
	data, err := json.Marshal(boot)
	if err != nil {
		http.Error(w, "web: encode the draft", http.StatusInternalServerError)
		return
	}
	page.Boot = string(data)
	renderEditor(w, "editor", page)
}

// editorTypes lists the node types: the builtin stages in pipeline order,
// then the custom types.
func editorTypes() []nodeTypeView {
	types := baseTypes()
	for i := range types {
		types[i].Kind = strings.ToLower(string(presentrun.NodeKindForStage(types[i].Name)))
	}
	return types
}

func baseTypes() []nodeTypeView {
	var types []nodeTypeView
	for _, node := range splicerun.DefaultTopology().Nodes {
		caps, _ := schemas.BuiltinCapabilities(node.Type)
		types = append(types, nodeTypeView{Name: node.Type, Builtin: true, ModelFree: caps.ModelFree, About: typeAbout[node.Type]})
	}
	return append(types,
		nodeTypeView{Name: schemas.NodeTypePrompt, About: typeAbout[schemas.NodeTypePrompt]},
		nodeTypeView{Name: schemas.NodeTypeCommand, ModelFree: true, About: typeAbout[schemas.NodeTypeCommand]},
		nodeTypeView{Name: schemas.NodeTypeAgent, About: typeAbout[schemas.NodeTypeAgent]})
}

// editorTools lists the tools an agent node may use, with the reach marks of
// the trust card.
func editorTools() []toolView {
	tools := make([]toolView, 0, len(schemas.AgentEligibleTools))
	for _, name := range schemas.AgentEligibleTools {
		marks := splicerun.DescribeAgentNode(schemas.PipelineNode{Tools: []string{name}}).Marks
		tools = append(tools, toolView{Name: name, Marks: strings.Join(marks, ", ")})
	}
	return tools
}

// editorPermissions lists the agent permission values with the rule of the
// trust card.
func editorPermissions() []permissionView {
	var out []permissionView
	for _, value := range []string{"", schemas.AgentPermissionAsk, schemas.AgentPermissionAuto} {
		d := splicerun.DescribeAgentNode(schemas.PipelineNode{Permission: value})
		out = append(out, permissionView{Value: value, Label: d.Permission, Rule: d.PermissionRule})
	}
	return out
}

func editorTiers() []string {
	return []string{string(schemas.TierTrivial), string(schemas.TierLight), string(schemas.TierStandard), string(schemas.TierSubstantial), string(schemas.TierArchitectural)}
}

func renderEditor(w http.ResponseWriter, name string, data any) {
	var out strings.Builder
	if err := editorTemplates.ExecuteTemplate(&out, name, data); err != nil {
		http.Error(w, "web: render the editor", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(out.String()))
}

func (e *Editor) serveSub(w http.ResponseWriter, r *http.Request, sub string) {
	switch sub {
	case "editor.css":
		serveAsset(w, "assets/editor.css", "text/css; charset=utf-8")
	case "editor.js":
		serveAsset(w, "assets/editor.js", "text/javascript; charset=utf-8")
	case "pipeline.css":
		serveAsset(w, "assets/pipeline.css", "text/css; charset=utf-8")
	case "favicon.svg":
		serveAsset(w, "assets/favicon.svg", "image/svg+xml")
	case "consent":
		e.serveConsent(w, r.URL.Query().Get("id"))
	default:
		http.NotFound(w, r)
	}
}

// serveConsent returns the publish dialog of a valid draft.
func (e *Editor) serveConsent(w http.ResponseWriter, id string) {
	d, ok := e.draftOr404(w, id)
	if !ok {
		return
	}
	p, check, err := preparePublish(d.Doc)
	if err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, err.Error(), &check)
		return
	}
	writeJSON(w, consentFor(p, check))
}

func (e *Editor) draftOr404(w http.ResponseWriter, id string) (draft, bool) {
	d, err := loadDraft(id)
	switch {
	case errors.Is(err, errNoDraft):
		writeProblem(w, http.StatusNotFound, "This draft does not exist. It was deleted, or the link is wrong.", nil)
		return draft{}, false
	case err != nil:
		writeProblem(w, http.StatusInternalServerError, err.Error(), nil)
		return draft{}, false
	}
	return d, true
}

// serveMutate routes the editor's writes. The server already checked the
// session cookie, the Origin, and the action token.
func (e *Editor) serveMutate(w http.ResponseWriter, r *http.Request) {
	sub := strings.TrimPrefix(r.URL.Path, "/s/"+EditorSurface+"/")
	id := r.URL.Query().Get("id")
	switch {
	case sub == "drafts" && r.Method == http.MethodPost:
		e.handleCreate(w, r)
	case sub == "draft" && r.Method == http.MethodPut:
		e.handleSave(w, r, id)
	case sub == "draft" && r.Method == http.MethodDelete:
		e.mu.Lock()
		err := deleteDraft(id)
		e.mu.Unlock()
		if errors.Is(err, errNoDraft) {
			writeProblem(w, http.StatusNotFound, "This draft does not exist.", nil)
			return
		}
		if err != nil {
			writeProblem(w, http.StatusInternalServerError, err.Error(), nil)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case sub == "publish" && r.Method == http.MethodPost:
		e.handlePublish(w, r, id)
	case sub == "use" && r.Method == http.MethodPost:
		e.handleUse(w, r)
	case sub == "layout" && r.Method == http.MethodPost:
		e.handleLayout(w, r)
	default:
		writeProblem(w, http.StatusNotFound, "web: no such editor action", nil)
	}
}

func (e *Editor) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From string `json:"from"`
		Name string `json:"name"`
		// New starts a fresh draft instead of the latest one of the source.
		New bool `json:"new"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	cfg := e.config()
	e.mu.Lock()
	d, err := createDraft(req.From, req.Name, cfg.Sources, req.New)
	e.mu.Unlock()
	if err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, err.Error(), nil)
		return
	}
	writeJSON(w, map[string]string{"id": d.ID})
}

// handleSave is autosave. It validates the document and saves it even when it
// is not valid, so no work is lost. The reply is the server's verdict.
func (e *Editor) handleSave(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		Doc       json.RawMessage `json:"doc"`
		Positions []position      `json:"positions"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if !isJSONObject(req.Doc) {
		writeProblem(w, http.StatusBadRequest, "doc must be a JSON object", nil)
		return
	}
	if len(req.Positions) > maxPositions {
		writeProblem(w, http.StatusBadRequest, fmt.Sprintf("positions: at most %d", maxPositions), nil)
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	d, ok := e.draftOr404(w, id)
	if !ok {
		return
	}
	check := checkDoc(req.Doc)
	d.Doc, d.Positions, d.Valid, d.Updated = req.Doc, cleanPositions(req.Positions), check.Valid, time.Now().UTC()
	if err := saveDraft(d); err != nil {
		writeProblem(w, http.StatusInternalServerError, "The draft did not save: "+err.Error(), nil)
		return
	}
	writeJSON(w, struct {
		editorCheck
		Saved string `json:"saved"`
	}{check, d.Updated.Local().Format("15:04:05")})
}

// handlePublish installs a valid draft in the user library. It needs an
// explicit confirm and the digest of the document that the dialog showed. A
// draft or a library that changed since that review is refused, so the
// confirm never covers a document that the user did not see.
func (e *Editor) handlePublish(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		Confirm bool   `json:"confirm"`
		Replace bool   `json:"replace"`
		Digest  string `json:"digest"`
		// OldDigest names the entry that the dialog's diff came from.
		OldDigest string `json:"old_digest"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	d, ok := e.draftOr404(w, id)
	if !ok {
		return
	}
	p, check, err := preparePublish(d.Doc)
	if err != nil {
		writeProblem(w, http.StatusUnprocessableEntity, err.Error(), &check)
		return
	}
	if !req.Confirm {
		writeProblem(w, http.StatusBadRequest, "Publish needs an explicit confirm after the review of the command and agent nodes.", nil)
		return
	}
	if req.Digest != p.digest || req.Replace != p.exists || req.OldDigest != p.oldDigest {
		writeProblem(w, http.StatusConflict, "The draft or the library changed after the review. Review it again.", nil)
		return
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		writeProblem(w, http.StatusInternalServerError, err.Error(), nil)
		return
	}
	if err := splicerun.WriteFileAtomic(p.path, p.data, 0o644); err != nil {
		writeProblem(w, http.StatusInternalServerError, err.Error(), nil)
		return
	}
	// The draft now edits the entry it made.
	d.Base, d.Source, d.Origin, d.Updated = p.topology.Name, "library entry "+p.topology.Name, fromLibrary+":"+p.topology.Name, time.Now().UTC()
	_ = saveDraft(d)
	writeJSON(w, map[string]any{"name": p.topology.Name, "path": p.path, "replaced": p.exists})
}

// handleLayout returns the viewer layout of a document, for Auto-layout. It
// writes nothing.
func (e *Editor) handleLayout(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Doc json.RawMessage `json:"doc"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	positions, err := layoutDoc(req.Doc)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, err.Error(), nil)
		return
	}
	writeJSON(w, map[string][]position{"positions": positions})
}

// handleUse makes a library entry active through the host, as `/pipeline use`.
func (e *Editor) handleUse(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	use := e.config().Use
	if use == nil {
		writeProblem(w, http.StatusNotImplemented, "This server cannot change the active pipeline. Run: splice pipeline use "+req.Name, nil)
		return
	}
	text, err := use(req.Name)
	if err != nil {
		writeProblem(w, http.StatusConflict, err.Error(), nil)
		return
	}
	writeJSON(w, map[string]string{"message": text})
}

// readJSON decodes one JSON request body into v. It refuses another content
// type, a body over maxEditorBody, an unknown field, and data after the value.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mediaType != "application/json" {
		writeProblem(w, http.StatusUnsupportedMediaType, "The request must be application/json.", nil)
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxEditorBody))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(v)
	if err == nil {
		if _, tail := decoder.Token(); !errors.Is(tail, io.EOF) {
			err = errors.New("unexpected data after the JSON value")
		}
	}
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		writeProblem(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("The request is over the %d-byte limit.", maxEditorBody), nil)
		return false
	case err != nil:
		writeProblem(w, http.StatusBadRequest, "The request does not parse: "+err.Error(), nil)
		return false
	}
	return true
}

// writeProblem writes an error reply. check carries the validation verdict
// when the error is a validation failure.
func writeProblem(w http.ResponseWriter, status int, message string, check *editorCheck) {
	data, _ := json.Marshal(struct {
		Error string       `json:"error"`
		Check *editorCheck `json:"check,omitempty"`
	}{message, check})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
