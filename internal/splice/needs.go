package splice

// Work package E1 (warm-cost handoff Section 9): explicit context needs and
// reuse resolutions.
//
// The discovery plan gains a typed need set: what the stage must know before
// it can edit, expressed as a small closed set of need kinds, and the
// resolution records that say which persisted cognition node answered which
// need and which concrete discovery operation the answer replaces.
//
// Invariants pinned here:
//
//   - Need kinds are limited and testable. Five structural kinds plus one
//     explicit open-ended need; nothing fuzzy.
//   - Explicit identifiers in task text (for example EnforceRetention) are
//     only treated as symbols when the current-source symbol index (B2's
//     go/parser extraction) actually declares them. A capitalized word is
//     never a symbol by itself.
//   - If the improved cold plan already locates an explicitly named helper
//     through the shared symbol index, warm receives NO credit for locating
//     it again: such needs carry Origin "cold-symbol-index" and are never
//     answered by a reuse resolution.
//   - Deriving N needs never proves there are only N requirements: the
//     open-ended need survives until every structural need is resolved and
//     the task named its own edit targets.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Taf0711/splice/internal/splice/cognition"
)

// Need kinds. The closed set E1 admits; validation rejects anything else.
const (
	NeedLocateNamedOperation              = "locate-named-operation"
	NeedInspectEditTarget                 = "inspect-edit-target"
	NeedObtainReferencedDeclaration       = "obtain-referenced-declaration"
	NeedUnderstandIntegrationRelationship = "understand-integration-relationship"
	NeedResolveConcreteFailure            = "resolve-concrete-failure"
	NeedOpenDiscovery                     = "open-discovery"
)

// validNeedKinds is the validation table for ContextNeed.Kind.
var validNeedKinds = map[string]bool{
	NeedLocateNamedOperation:              true,
	NeedInspectEditTarget:                 true,
	NeedObtainReferencedDeclaration:       true,
	NeedUnderstandIntegrationRelationship: true,
	NeedResolveConcreteFailure:            true,
	NeedOpenDiscovery:                     true,
}

// Need origin values. Origin records WHO established the need, which is what
// the no-double-credit rule reads.
const (
	// NeedOriginTaskText: the distilled task text names the subject.
	NeedOriginTaskText = "task-text"
	// NeedOriginSymbolIndex: an explicit identifier from the task text was
	// confirmed in the shared current-source symbol index. The improved
	// cold plan locates it through the same index, so warm gets no credit
	// for re-locating it.
	NeedOriginSymbolIndex = "cold-symbol-index"
	// NeedOriginPriorEvidence: structured prior stage evidence (changed
	// files, verification commands) establishes the need.
	NeedOriginPriorEvidence = "prior-evidence"
	// NeedOriginFailureEvidence: recorded failure evidence from a prior
	// stage or run establishes the need.
	NeedOriginFailureEvidence = "failure-evidence"
	// NeedOriginStructural: the open-ended need that exists because
	// structure alone cannot prove the task is fully specified.
	NeedOriginStructural = "structural"
)

// ContextNeed is one thing a stage must know before it can edit correctly.
type ContextNeed struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Subject  string `json:"subject"`
	Origin   string `json:"origin"`
	Required bool   `json:"required"`
}

// ReuseResolution records one admissible answer from persisted cognition:
// which need it answers, which record answers it, which current-source views
// carry the evidence needed to USE the answer, and which concrete cold-plan
// operations the answer replaces. A resolution without replaced operations is
// delivery, not substitution, and must never be counted as avoided work.
type ReuseResolution struct {
	NeedID             string   `json:"need_id"`
	RecordRef          string   `json:"record_ref"`
	RecordDigest       string   `json:"record_digest"`
	SupportingViews    []string `json:"supporting_views,omitempty"`
	ReplacedOperations []string `json:"replaced_operations,omitempty"`
	Applicability      string   `json:"applicability,omitempty"`
}

// Validate checks one need against the closed kind table.
func (n ContextNeed) Validate() error {
	if n.ID == "" {
		return errNeedField("id")
	}
	if !validNeedKinds[n.Kind] {
		return errNeedField("kind " + n.Kind)
	}
	if n.Subject == "" && n.Kind != NeedOpenDiscovery {
		return errNeedField("subject")
	}
	if n.Origin == "" {
		return errNeedField("origin")
	}
	return nil
}

type needError string

func (e needError) Error() string { return "context need: invalid " + string(e) }

func errNeedField(what string) error { return needError(what) }

// coldResolved reports whether the improved cold plan answers this need
// itself (through the shared symbol index), so warm must not claim credit.
func (n ContextNeed) coldResolved() bool { return n.Origin == NeedOriginSymbolIndex }

// identifierToken matches a Go-identifier-shaped word: it starts with a
// letter or underscore. Only these are candidates for symbol confirmation;
// the confirmation step (the symbol index) does the real filtering, so a
// capitalized English word costs one index lookup and nothing more.
var identifierToken = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)

// symbolIndex is the current-source symbol index built over the workspace
// with go/parser (the B2 extraction path). It maps declared top-level symbol
// names to the repo-relative files that declare them, so task-text
// identifiers can be confirmed against real source instead of guessed.
type symbolIndex struct {
	byName map[string][]string
}

// buildSymbolIndex walks workspace Go files (production sources only; test
// files are excluded so a trap test cannot plant a phantom symbol) and
// records every top-level function, method, and type declaration. Parse
// failures are skipped: the index is best-effort evidence, never a gate.
func buildSymbolIndex(workspace string) *symbolIndex {
	idx := &symbolIndex{byName: map[string][]string{}}
	if workspace == "" {
		return idx
	}
	files := productionGoFiles(workspace)
	for _, rel := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(workspace, rel), nil, 0)
		if err != nil {
			continue
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Name == nil || d.Name.Name == "" {
					continue
				}
				name := d.Name.Name
				if d.Recv != nil && len(d.Recv.List) > 0 {
					if recv := receiverTypeNameString(d.Recv.List[0].Type); recv != "" {
						name = recv + "." + name
					}
				}
				idx.add(name, rel)
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || ts.Name == nil {
						continue
					}
					idx.add(ts.Name.Name, rel)
				}
			}
		}
	}
	return idx
}

func (i *symbolIndex) add(name, file string) {
	i.byName[name] = append(i.byName[name], file)
}

// lookup returns the files declaring name, matching either a top-level
// declaration of exactly name or a method whose receiver-qualified name ends
// in ".name". Task text uses bare names; the index stores receiver-qualified
// method names.
func (i *symbolIndex) lookup(name string) []string {
	if files, ok := i.byName[name]; ok {
		return files
	}
	suffix := "." + name
	var files []string
	for qualified, fs := range i.byName {
		if strings.HasSuffix(qualified, suffix) {
			files = append(files, fs...)
		}
	}
	sort.Strings(files)
	return files
}

// productionGoFiles lists repo-relative .go production files under workspace,
// skipping hidden, vendor, and test-named files. Bounded walk; sorted output.
func productionGoFiles(workspace string) []string {
	var out []string
	_ = filepath.WalkDir(workspace, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != workspace && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || isTestGoFile(d.Name()) {
			return nil
		}
		rel, rerr := filepath.Rel(workspace, path)
		if rerr != nil || rel == "." {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out)
	return out
}

func isTestGoFile(name string) bool {
	return strings.HasSuffix(name, "_test.go")
}

// deriveContextNeeds forms the typed need set for one stage from the task
// intent, structured prior evidence, and the derived cognition keys. It is
// deterministic and model-free.
//
// Mapping (documented, testable):
//
//   - explicit identifier confirmed in the symbol index -> locate-named-
//     operation with Origin cold-symbol-index (cold already resolved it);
//   - an unconfirmed capitalized identifier is NOT a need: prose is not a
//     symbol (the no-capitalized-word rule);
//   - symbol keys from task text (path#Sym) -> locate-named-operation
//     (Origin task-text);
//   - file keys from task text -> inspect-edit-target;
//   - file keys from prior changed files -> understand-integration-
//     relationship (the edit must integrate with what earlier stages built);
//   - package keys from verification commands -> obtain-referenced-
//     declaration (the verification target's declarations must be known);
//   - prior failure evidence -> resolve-concrete-failure;
//   - always: one open-ended need when structure cannot prove full coverage.
func deriveContextNeeds(intent string, workspace string, priorFiles []string, failureEvidence []string) []ContextNeed {
	needs := []ContextNeed{}
	seen := map[string]bool{}
	add := func(n ContextNeed) {
		if seen[n.Kind+"\x00"+n.Subject] {
			return
		}
		seen[n.Kind+"\x00"+n.Subject] = true
		needs = append(needs, n)
	}

	// Explicit identifiers from the task text, confirmed against the
	// current-source index. Cold resolves these through the same index, so
	// their origin marks them cold-resolved: warm gets no credit.
	idx := buildSymbolIndex(workspace)
	candidates := identifierCandidates(intent)
	sort.Strings(candidates)
	for _, name := range candidates {
		files := idx.lookup(name)
		if len(files) == 0 {
			continue
		}
		// Ambiguous ownership (two files declare the name) stays a need,
		// but no unique symbol key may be minted; the subject carries the
		// ambiguity explicitly.
		subject := name
		if len(files) == 1 {
			subject = files[0] + "#" + name
		} else {
			// Ambiguous ownership: the need stays, but no unique symbol
			// key may be minted. The ambiguity is explicit in the subject.
			subject = name + " (declared in " + strings.Join(files, ", ") + ")"
		}
		add(ContextNeed{
			ID:       needID("locate", subject),
			Kind:     NeedLocateNamedOperation,
			Subject:  subject,
			Origin:   NeedOriginSymbolIndex,
			Required: true,
		})
	}

	// Strict path#Symbol tokens from the task text (existing conservative
	// miner): warm may answer these; cold's default request does not
	// resolve path#Sym tokens today.
	for _, sym := range strictTaskSymbols(intent) {
		add(ContextNeed{
			ID:       needID("locate", sym),
			Kind:     NeedLocateNamedOperation,
			Subject:  sym,
			Origin:   NeedOriginTaskText,
			Required: true,
		})
	}

	// Edit targets named by the task text.
	for _, path := range strictTaskPaths(intent) {
		add(ContextNeed{
			ID:       needID("inspect", path),
			Kind:     NeedInspectEditTarget,
			Subject:  path,
			Origin:   NeedOriginTaskText,
			Required: true,
		})
	}

	// Prior changed files: the integration surface.
	for _, path := range priorFiles {
		if path == "" {
			continue
		}
		add(ContextNeed{
			ID:       needID("integrate", path),
			Kind:     NeedUnderstandIntegrationRelationship,
			Subject:  path,
			Origin:   NeedOriginPriorEvidence,
			Required: true,
		})
	}

	// Recorded failure evidence: a concrete failure the stage must resolve.
	for i, ev := range failureEvidence {
		if strings.TrimSpace(ev) == "" {
			continue
		}
		add(ContextNeed{
			ID:       needID("failure", ev),
			Kind:     NeedResolveConcreteFailure,
			Subject:  ev,
			Origin:   NeedOriginFailureEvidence,
			Required: true,
		})
		_ = i
	}

	// The open-ended need: structure never proves the task is fully
	// specified. Required=false: it constrains discovery, not acceptance.
	add(ContextNeed{
		ID:       needID("open", truncateSubject(intent)),
		Kind:     NeedOpenDiscovery,
		Subject:  truncateSubject(intent),
		Origin:   NeedOriginStructural,
		Required: false,
	})

	return needs
}

// identifierCandidates extracts identifier-shaped words from the task text
// for index confirmation. Everything word-like is a candidate; only the
// index decides which are real symbols.
func identifierCandidates(intent string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range identifierToken.FindAllString(intent, -1) {
		if seen[m] || len(m) < 4 {
			continue
		}
		// Skip obvious prose stopwords and file-extension fragments; the
		// index would reject them anyway, but bounding the candidate set
		// keeps the walk cheap.
		switch strings.ToLower(m) {
		case "this", "that", "with", "from", "must", "when", "then", "test", "tests",
			"package", "function", "method", "return", "error", "true", "false", "none":
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	return out
}

// cognitionKeysForNeeds derives strict keys from prose via the shared
// conservative cognition miner.
func cognitionKeysForNeeds(intent string, priorFiles []string) []string {
	prior := map[string][]string{}
	if len(priorFiles) > 0 {
		prior["prior"] = priorFiles
	}
	return cognition.DeriveKeys(cognition.DeriveInput{
		RequestIntent:     intent,
		PriorChangedFiles: prior,
	})
}

// strictTaskSymbols and strictTaskPaths reuse the conservative miners from
// the cognition package via the shared key derivation: they mine strict
// path#Symbol and repo-relative path tokens from prose.
func strictTaskSymbols(intent string) []string {
	var out []string
	for _, key := range cognitionKeysForNeeds(intent, nil) {
		if rest, ok := trimKeyPrefix(key, "symbol:"); ok {
			out = append(out, rest)
		}
	}
	return out
}

func strictTaskPaths(intent string) []string {
	var out []string
	for _, key := range cognitionKeysForNeeds(intent, nil) {
		if rest, ok := trimKeyPrefix(key, "file:"); ok {
			out = append(out, rest)
		}
	}
	return out
}

func trimKeyPrefix(key, prefix string) (string, bool) {
	if len(key) > len(prefix) && key[:len(prefix)] == prefix {
		return key[len(prefix):], true
	}
	return "", false
}

func needID(kind, subject string) string {
	return fmt.Sprintf("%s:%s", kind, truncateSubject(subject))
}

func truncateSubject(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 80 {
		s = s[:77] + "..."
	}
	return s
}
