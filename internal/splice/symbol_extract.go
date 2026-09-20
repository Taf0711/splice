package splice

// Work package B2 (warm-cost handoff Section 6): deterministic Go symbol
// extraction over the guarded source seam.
//
// The seam (B1) acquires raw bytes through the tool boundary; this file
// parses them with go/parser and go/token. A path-qualified query resolves
// inside that file; a bare name searches the bounded set of production
// files the discovery planner already knows and refuses to pick between
// same-named declarations (typed unresolved evidence instead). Returns are
// the declaration span plus bounded import context; this is never claimed
// to be a complete semantic dependency closure.

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

// SymbolResolution is the typed result of one get_symbol attempt. Exactly
// one of Resolved/Unresolved is set: a resolution carries the declaration
// view and bounded context; an unresolved outcome names WHY, so the caller
// can fall back to bounded reads/searches instead of guessing.
type SymbolResolution struct {
	// Path and range identity the declaration.
	Path      string
	Version   string
	StartLine int
	EndLine   int
	// Decl is the declaration text exactly as it appears in the source
	// (raw bytes of the span, no reformatting).
	Decl string
	// Imports lists the file's import paths (bounded context, not a
	// dependency closure).
	Imports []string
	// Receiver is the receiver type for methods ("T" or "*T").
	Receiver string
	// Kind is the Go declaration kind: func | method | type | var | const.
	Kind string

	// Unresolved carries the typed failure. Empty on resolution.
	Unresolved    string
	UnresolvedWhy string // ambiguous | not_found | parse_error | unsupported
	// Candidates names the conflicting locations for ambiguous outcomes,
	// so the caller can disambiguate by path instead of trusting a pick.
	Candidates []string
}

// symbolExtractionResult couples the resolution with the snapshot the
// declaration was parsed from, so the caller can materialize views without
// re-reading.
type symbolExtractionResult struct {
	Resolution SymbolResolution
	Snapshots  map[string]SourceSnapshot
}

// extractGoSymbol resolves name in the given path. When path is empty, the
// candidate file set is searched and same-named declarations across files
// stay ambiguous (typed evidence, never an arbitrary pick).
func extractGoSymbol(ctx context.Context, cache *sourceCache, reader SourceReader, name, path string) (symbolExtractionResult, error) {
	if strings.TrimSpace(name) == "" {
		return symbolExtractionResult{}, fmt.Errorf("get_symbol: empty symbol name")
	}
	var paths []string
	if path != "" {
		paths = []string{path}
	} else {
		// Bare-name search over the workspace's Go files comes later in
		// the fallback chain; without a path, the caller gets typed
		// unsupported evidence pointing at find_symbol + read_file.
		return symbolExtractionResult{
			Resolution: SymbolResolution{
				Unresolved:    fmt.Sprintf("get_symbol %q requires a path-qualified query; use find_symbol to locate the file first", name),
				UnresolvedWhy: "unsupported",
			},
		}, nil
	}
	result := symbolExtractionResult{Snapshots: map[string]SourceSnapshot{}}
	type hit struct {
		path      string
		snap      SourceSnapshot
		startLine int
		endLine   int
		kind      string
		receiver  string
	}
	var hits []hit
	imports := map[string][]string{}
	parseFailures := []string{}
	for _, p := range paths {
		snap, err := cache.get(ctx, reader, p)
		if err != nil {
			return symbolExtractionResult{}, fmt.Errorf("get_symbol %s: %w", p, err)
		}
		result.Snapshots[p] = snap
		fileSet := token.NewFileSet()
		// go/parser over RAW bytes acquired through the seam: line
		// numbers map 1:1 to the file's real lines.
		file, err := parser.ParseFile(fileSet, p, snap.Raw, parser.SkipObjectResolution)
		if err != nil {
			parseFailures = append(parseFailures, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		var fileImports []string
		for _, imp := range file.Imports {
			imports[p] = append(imports[p], imp.Path.Value)
			fileImports = append(fileImports, imp.Path.Value)
		}
		_ = fileImports
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				start := fileSet.Position(d.Pos()).Line
				end := fileSet.Position(d.End()).Line
				kind := "func"
				receiver := ""
				if d.Recv != nil && len(d.Recv.List) > 0 {
					kind = "method"
					receiver = receiverTypeName(d.Recv.List[0].Type)
				}
				if d.Name.Name == name {
					hits = append(hits, hit{path: p, snap: snap, startLine: start, endLine: end, kind: kind, receiver: receiver})
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.Name == name {
							hits = append(hits, hit{
								path:      p,
								snap:      snap,
								startLine: fileSet.Position(s.Pos()).Line,
								endLine:   fileSet.Position(d.End()).Line,
								kind:      "type",
							})
						}
					case *ast.ValueSpec:
						for _, vn := range s.Names {
							if vn.Name == name {
								kind := "var"
								if d.Tok == token.CONST {
									kind = "const"
								}
								hits = append(hits, hit{
									path:      p,
									snap:      snap,
									startLine: fileSet.Position(s.Pos()).Line,
									endLine:   fileSet.Position(s.End()).Line,
									kind:      kind,
								})
							}
						}
					}
				}
			}
		}
	}
	if len(parseFailures) > 0 {
		return symbolExtractionResult{
			Resolution: SymbolResolution{
				Unresolved:    fmt.Sprintf("get_symbol %q: parse failed: %s", name, strings.Join(parseFailures, "; ")),
				UnresolvedWhy: "parse_error",
			},
		}, nil
	}
	if len(hits) == 0 {
		return symbolExtractionResult{
			Resolution: SymbolResolution{
				Unresolved:    fmt.Sprintf("get_symbol %q: no declaration named %s in %s", name, name, strings.Join(paths, ", ")),
				UnresolvedWhy: "not_found",
			},
		}, nil
	}
	if len(hits) > 1 {
		// Same-named declarations across the searched set: refuse to pick.
		// The candidates let the caller re-query with a specific path.
		candidates := make([]string, 0, len(hits))
		for _, h := range hits {
			candidates = append(candidates, fmt.Sprintf("%s:%d (%s)", h.path, h.startLine, h.kind))
		}
		sort.Strings(candidates)
		return symbolExtractionResult{
			Resolution: SymbolResolution{
				Unresolved:    fmt.Sprintf("get_symbol %q is ambiguous across %d declarations; re-query with an explicit path", name, len(hits)),
				UnresolvedWhy: "ambiguous",
				Candidates:    candidates,
			},
		}, nil
	}
	only := hits[0]
	declView, err := BuildSourceView(only.snap, only.startLine, only.endLine, "symbol:"+name)
	if err != nil {
		return symbolExtractionResult{}, fmt.Errorf("get_symbol %s: %w", name, err)
	}
	// Bounded import context: the file's own imports only, sorted for
	// determinism. This is NOT a dependency closure.
	sort.Strings(imports[only.path])
	resolution := SymbolResolution{
		Path:      only.path,
		Version:   only.snap.SHA256,
		StartLine: only.startLine,
		EndLine:   only.endLine,
		Decl:      declView.Text,
		Imports:   imports[only.path],
		Receiver:  only.receiver,
		Kind:      only.kind,
	}
	return symbolExtractionResult{Resolution: resolution, Snapshots: result.Snapshots}, nil
}

// receiverTypeName renders a receiver type expression as its name,
// including the pointer marker. Reuses the existing helper in
// diagnostics.go (same semantics: Ident/Star/generic unwrap).
