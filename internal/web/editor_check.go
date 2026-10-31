package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	splicerun "github.com/Taf0711/splice/internal/splice"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// editorProblem is one validation error, placed where the page shows it.
//
//   - index: the node index in the document, when a node has the problem.
//   - node: that node's name.
//   - edge: the edge index, when an edge has the problem.
//   - field: the JSON field, such as "tools" or "budget". Empty means the
//     node, the edge, or the topology as a whole.
//   - tier: the tier, when the problem exists only at that tier.
//   - message: the loader's words, without the location prefixes.
type editorProblem struct {
	Index   *int   `json:"index,omitempty"`
	Node    string `json:"node,omitempty"`
	Edge    *int   `json:"edge,omitempty"`
	Field   string `json:"field,omitempty"`
	Tier    string `json:"tier,omitempty"`
	Message string `json:"message"`
}

// editorCheck is the server's verdict on one document. Valid means that
// publish can install it: it passes splicerun.DecodeTopology, which runs the
// schema, the compile at every tier, and the envelope check, and it passes
// the library name rule. That is the same check as `splice pipeline import`.
type editorCheck struct {
	Valid    bool            `json:"valid"`
	Problems []editorProblem `json:"problems"`
	Warnings []string        `json:"warnings"`
	// Commands give each command node's exact command line, so the panel shows
	// what runs.
	Commands []commandView `json:"commands"`
	// Document is the exact file that publish writes. It is set only when
	// the document is valid.
	Document string `json:"document,omitempty"`
}

type commandView struct {
	Name string `json:"name"`
	Line string `json:"line"`
}

// checkDoc validates a document. The verdict is DecodeTopology's. Before it,
// each node is checked on its own, so the page can show the problems of all
// nodes at once, not only the first one.
func checkDoc(doc []byte) editorCheck {
	check := editorCheck{Problems: []editorProblem{}, Warnings: []string{}, Commands: []commandView{}}
	var loose schemas.PipelineTopology
	looseOK := json.Unmarshal(doc, &loose) == nil
	if looseOK {
		for _, command := range splicerun.CommandNodes(&loose) {
			check.Commands = append(check.Commands, commandView{Name: command.Name, Line: command.Line})
		}
		for i, node := range loose.Nodes {
			if err := node.Validate(); err != nil {
				check.Problems = append(check.Problems, problemFor(fmt.Sprintf("nodes[%d]: %v", i, err), doc, &loose))
			}
		}
	}
	topology, err := splicerun.DecodeTopology(doc)
	if err != nil {
		p := problemFor(err.Error(), doc, &loose)
		if !hasProblem(check.Problems, p) {
			check.Problems = append(check.Problems, p)
		}
		return check
	}
	if _, err := splicerun.LibraryEntryPath(topology.Name); err != nil {
		check.Problems = append(check.Problems, editorProblem{Field: "name", Message: err.Error()})
		return check
	}
	check.Valid = len(check.Problems) == 0
	if warnings, err := splicerun.TopologyWarnings(&topology); err == nil {
		check.Warnings = append(check.Warnings, warnings...)
	}
	if data, err := publishBytes(topology); err == nil {
		check.Document = string(data)
	}
	return check
}

func hasProblem(problems []editorProblem, p editorProblem) bool {
	for _, have := range problems {
		if have.Message == p.Message && intIs(have.Index, p.Index) && intIs(have.Edge, p.Edge) {
			return true
		}
	}
	return false
}

func intIs(a, b *int) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// The location prefixes of the loader's errors. TestEditorProblemsPlaceEveryLoaderError
// pairs them with the errors that the schema and the compiler produce.
var (
	nodeIndexPrefix  = regexp.MustCompile(`^nodes\[(\d+)\]: `)
	edgeIndexPrefix  = regexp.MustCompile(`^edges\[(\d+)\](?: \([^)]*\))?: `)
	tierPrefix       = regexp.MustCompile(`^tier (\w+): topology [^:]+: `)
	topologyPrefix   = regexp.MustCompile(`^topology [a-z0-9_-]+: `)
	activeDependency = regexp.MustCompile(`node (.+) is active at tier "(\w+)" but depends on `)
	unknownField     = regexp.MustCompile(`unknown field "([^"]+)"`)
	typeMismatch     = regexp.MustCompile(`Go struct field [A-Za-z]+\.([a-z_.]+) of type`)
)

// problemFor places one loader error: the node or edge it names, the field,
// and the tier.
func problemFor(text string, doc []byte, loose *schemas.PipelineTopology) editorProblem {
	p := editorProblem{Message: text}
	nodeAt := func(i int) {
		p.Index = &i
		if i < len(loose.Nodes) {
			p.Node = loose.Nodes[i].Name
		}
	}
	if m := nodeIndexPrefix.FindStringSubmatch(text); m != nil {
		i, _ := strconv.Atoi(m[1])
		nodeAt(i)
		rest := strings.TrimPrefix(text, m[0])
		if p.Node != "" {
			rest = strings.TrimPrefix(rest, "node "+p.Node+": ")
		}
		p.Message, p.Field = rest, nodeField(rest)
		return p
	}
	if m := edgeIndexPrefix.FindStringSubmatch(text); m != nil {
		i, _ := strconv.Atoi(m[1])
		p.Edge, p.Message = &i, strings.TrimPrefix(text, m[0])
		return p
	}
	if m := tierPrefix.FindStringSubmatch(text); m != nil {
		p.Tier, p.Message = m[1], strings.TrimPrefix(text, m[0])
		if strings.Contains(p.Message, "budget") {
			p.Field = "budget"
		}
		return p
	}
	if m := activeDependency.FindStringSubmatch(text); m != nil {
		for i, node := range loose.Nodes {
			if node.Name == m[1] {
				nodeAt(i)
				break
			}
		}
		p.Tier, p.Field = m[2], "tiers"
		p.Message = topologyPrefix.ReplaceAllString(text, "")
		return p
	}
	if m := unknownField.FindStringSubmatch(text); m != nil {
		p.Field = m[1]
		if i := nodeWithKey(doc, m[1]); i >= 0 {
			nodeAt(i)
		}
		p.Message = strings.TrimPrefix(text, "parse: json: ")
		return p
	}
	if m := typeMismatch.FindStringSubmatch(text); m != nil {
		parts := strings.Split(m[1], ".")
		p.Field = parts[len(parts)-1]
		if parts[0] == "nodes" {
			if i := nodeWithBadValue(doc, strings.Join(parts[1:], ".")); i >= 0 {
				nodeAt(i)
			}
		}
		p.Message = strings.TrimPrefix(text, "parse: json: ")
		return p
	}
	p.Message = topologyPrefix.ReplaceAllString(text, "")
	switch {
	case strings.Contains(text, "splice_min_version"), strings.Contains(text, "requires Splice"):
		p.Field = "splice_min_version"
	case strings.Contains(text, "cycle detected"):
		p.Field = "edges"
	case strings.Contains(text, "budget.total_"):
		p.Field = "budget"
	case strings.Contains(text, "pipeline name"), strings.Contains(text, "topology name"):
		p.Field = "name"
	case strings.HasPrefix(text, "topology version"):
		p.Field = "version"
	case strings.Contains(text, "at least one node"):
		p.Field = "nodes"
	}
	return p
}

// nodeFieldRules map a node error to its field. The first rule that matches
// wins, so a rule for a word that other errors also use comes late.
var nodeFieldRules = []struct{ text, field string }{
	{"tiers[", "tiers"},
	{"budget", "budget"},
	{"max_turns must", "max_turns"},
	{"permission must", "permission"},
	{"are only valid for type", "type"},
	{"model_free", "capabilities"},
	{"produces_verification", "capabilities"},
	{"pull_context", "capabilities"},
	{"pull_memory", "capabilities"},
	{"model declaration", "model"},
	{"model:", "model"},
	{"tool", "tools"},
	{"prompt", "prompt"},
	{"command", "command"},
	{"node type", "type"},
	{"node name", "name"},
}

func nodeField(message string) string {
	for _, rule := range nodeFieldRules {
		if strings.Contains(message, rule.text) {
			return rule.field
		}
	}
	return ""
}

// nodeWithKey returns the index of the first node that has the JSON key, or -1.
func nodeWithKey(doc []byte, key string) int {
	var raw struct {
		Nodes []map[string]json.RawMessage `json:"nodes"`
	}
	if json.Unmarshal(doc, &raw) != nil {
		return -1
	}
	for i, node := range raw.Nodes {
		if _, ok := node[key]; ok {
			return i
		}
		for _, inner := range []string{"model", "budget", "capabilities"} {
			var sub map[string]json.RawMessage
			if json.Unmarshal(node[inner], &sub) == nil {
				if _, ok := sub[key]; ok {
					return i
				}
			}
		}
	}
	return -1
}

// nodeWithBadValue returns the index of the first node whose value at the
// field path (such as "max_turns" or "budget.input_max") does not decode into
// the field's type, or -1. Other nodes can set the same key with a good value.
func nodeWithBadValue(doc []byte, path string) int {
	var raw struct {
		Nodes []json.RawMessage `json:"nodes"`
	}
	if json.Unmarshal(doc, &raw) != nil {
		return -1
	}
	for i, node := range raw.Nodes {
		var decoded schemas.PipelineNode
		var typeErr *json.UnmarshalTypeError
		if errors.As(json.Unmarshal(node, &decoded), &typeErr) && typeErr.Field == path {
			return i
		}
	}
	return -1
}

// publishable is a valid draft in the form that publish writes: the decoded
// topology, indented. The digest names exactly those bytes, so a confirm
// applies to the document that the user reviewed and to no other.
type publishable struct {
	topology schemas.PipelineTopology
	data     []byte
	digest   string
	path     string
	exists   bool
	// oldDigest names the library entry that publish replaces, or "" when
	// there is none, so a confirm never covers a diff against another file.
	oldDigest string
}

// publishBytes is the library file of a valid topology: the decoded
// topology, indented.
func publishBytes(topology schemas.PipelineTopology) ([]byte, error) {
	data, err := json.MarshalIndent(topology, "", "  ")
	return append(data, '\n'), err
}

var errInvalidDraft = errors.New("the draft has problems; fix them before you publish")

func preparePublish(doc []byte) (publishable, editorCheck, error) {
	check := checkDoc(doc)
	if !check.Valid {
		return publishable{}, check, errInvalidDraft
	}
	topology, err := splicerun.DecodeTopology(doc)
	if err != nil {
		return publishable{}, check, err
	}
	data, err := publishBytes(topology)
	if err != nil {
		return publishable{}, check, err
	}
	path, err := splicerun.LibraryEntryPath(topology.Name)
	if err != nil {
		return publishable{}, check, err
	}
	sum := sha256.Sum256(data)
	p := publishable{topology: topology, data: data, digest: hex.EncodeToString(sum[:]), path: path}
	if old, err := os.ReadFile(path); err == nil {
		oldSum := sha256.Sum256(old)
		p.exists, p.oldDigest = true, hex.EncodeToString(oldSum[:])
	} else if !errors.Is(err, os.ErrNotExist) {
		return publishable{}, check, fmt.Errorf("read the library entry %s: %w", path, err)
	}
	return p, check, nil
}

// editorConsent is the publish dialog. It discloses every command node and
// every agent node with the same functions as the trust card, `pipeline
// import`, and the viewer. Nothing here is a second disclosure.
type editorConsent struct {
	Name         string        `json:"name"`
	Path         string        `json:"path"`
	Replaces     bool          `json:"replaces"`
	Diff         *topologyDiff `json:"diff,omitempty"`
	DiffNote     string        `json:"diff_note,omitempty"`
	Commands     []commandView `json:"commands"`
	CommandCount string        `json:"command_count"`
	Agents       []agentJSON   `json:"agents"`
	AgentCount   string        `json:"agent_count"`
	AgentBudget  string        `json:"agent_budget"`
	Warnings     []string      `json:"warnings"`
	Digest       string        `json:"digest"`
	OldDigest    string        `json:"old_digest,omitempty"`
}

type agentJSON struct {
	Name       string `json:"name"`
	Tools      string `json:"tools"`
	Permission string `json:"permission"`
	Turns      int    `json:"turns"`
	Budget     string `json:"budget"`
	Prompt     string `json:"prompt"`
	// PromptFull is the whole prompt. The browser shows it as text in a
	// scroll box, after the shared bounded preview. Terminal surfaces keep
	// the one-line preview only.
	PromptFull string `json:"prompt_full"`
}

func consentFor(p publishable, check editorCheck) editorConsent {
	c := editorConsent{
		Name:      p.topology.Name,
		Path:      p.path,
		Replaces:  p.exists,
		Commands:  []commandView{},
		Agents:    []agentJSON{},
		Warnings:  check.Warnings,
		Digest:    p.digest,
		OldDigest: p.oldDigest,
	}
	for _, command := range splicerun.CommandNodes(&p.topology) {
		c.Commands = append(c.Commands, commandView{Name: command.Name, Line: command.Line})
	}
	if len(c.Commands) > 0 {
		c.CommandCount = splicerun.CommandNodeCountText(len(c.Commands))
	}
	var prompts []string
	for _, node := range p.topology.Nodes {
		if node.Type == schemas.NodeTypeAgent {
			prompts = append(prompts, node.Prompt)
		}
	}
	for i, disclosure := range splicerun.AgentNodes(&p.topology) {
		a := agentViewOf(disclosure)
		c.Agents = append(c.Agents, agentJSON{Name: a.Name, Tools: a.Tools, Permission: a.Permission, Turns: a.Turns, Budget: a.Budget, Prompt: a.Prompt, PromptFull: visibleText(prompts[i])})
	}
	if len(c.Agents) > 0 {
		c.AgentCount = splicerun.AgentNodeCountText(len(c.Agents))
		c.AgentBudget = splicerun.AgentBudgetSumText(&p.topology)
	}
	if p.exists {
		old, err := splicerun.LoadTopologyFile(p.path, "")
		if err != nil {
			c.DiffNote = "The current entry does not load (" + err.Error() + "). Publish replaces it."
		} else {
			d := diffTopology(old, &p.topology)
			c.Diff = &d
		}
	}
	return c
}

// visibleText removes the control and format characters of a text, such as
// bidirectional overrides, and keeps newlines and tabs. The page shows the
// rest as text, so a prompt cannot hide or reorder what the reviewer reads.
func visibleText(s string) string {
	return strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && (unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r)) {
			return -1
		}
		return r
	}, s)
}

// topologyDiff is the structural change from the library entry to the draft.
// A changed node lists the fields that changed. A changed edge has another
// payload.
type topologyDiff struct {
	NodesAdded   []string `json:"nodes_added"`
	NodesRemoved []string `json:"nodes_removed"`
	NodesChanged []string `json:"nodes_changed"`
	EdgesAdded   []string `json:"edges_added"`
	EdgesRemoved []string `json:"edges_removed"`
	EdgesChanged []string `json:"edges_changed"`
	Settings     []string `json:"settings"`
}

func diffTopology(old, cur *schemas.PipelineTopology) topologyDiff {
	d := topologyDiff{NodesAdded: []string{}, NodesRemoved: []string{}, NodesChanged: []string{}, EdgesAdded: []string{}, EdgesRemoved: []string{}, EdgesChanged: []string{}, Settings: []string{}}
	oldNodes := map[string]schemas.PipelineNode{}
	for _, node := range old.Nodes {
		oldNodes[node.Name] = node
	}
	curNodes := map[string]bool{}
	for _, node := range cur.Nodes {
		curNodes[node.Name] = true
		before, ok := oldNodes[node.Name]
		if !ok {
			d.NodesAdded = append(d.NodesAdded, node.Name)
			continue
		}
		if fields := changedKeys(before, node); len(fields) > 0 {
			d.NodesChanged = append(d.NodesChanged, node.Name+" ("+strings.Join(fields, ", ")+")")
		}
	}
	for _, node := range old.Nodes {
		if !curNodes[node.Name] {
			d.NodesRemoved = append(d.NodesRemoved, node.Name)
		}
	}
	edgeKey := func(e schemas.PipelineEdge) string { return e.From + " -> " + e.To }
	oldEdges := map[string]schemas.PipelineEdge{}
	for _, edge := range old.Edges {
		oldEdges[edgeKey(edge)] = edge
	}
	curEdges := map[string]bool{}
	for _, edge := range cur.Edges {
		key := edgeKey(edge)
		curEdges[key] = true
		before, ok := oldEdges[key]
		switch {
		case !ok:
			d.EdgesAdded = append(d.EdgesAdded, key)
		case before.Payload.Effective() != edge.Payload.Effective():
			d.EdgesChanged = append(d.EdgesChanged, fmt.Sprintf("%s (%s to %s)", key, before.Payload.Effective(), edge.Payload.Effective()))
		}
	}
	for _, edge := range old.Edges {
		if !curEdges[edgeKey(edge)] {
			d.EdgesRemoved = append(d.EdgesRemoved, edgeKey(edge))
		}
	}
	oldTop, curTop := *old, *cur
	oldTop.Nodes, oldTop.Edges, curTop.Nodes, curTop.Edges = nil, nil, nil, nil
	d.Settings = append(d.Settings, changedKeys(oldTop, curTop)...)
	return d
}

// changedKeys returns the JSON keys whose values differ, in the order of b.
func changedKeys(a, b any) []string {
	var am, bm map[string]json.RawMessage
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	_ = json.Unmarshal(aj, &am)
	_ = json.Unmarshal(bj, &bm)
	var keys []string
	seen := map[string]bool{}
	for _, raw := range []map[string]json.RawMessage{bm, am} {
		for key := range raw {
			if seen[key] {
				continue
			}
			seen[key] = true
			if !bytes.Equal(am[key], bm[key]) {
				keys = append(keys, key)
			}
		}
	}
	sort.Strings(keys)
	return keys
}
