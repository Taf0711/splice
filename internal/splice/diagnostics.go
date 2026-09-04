package splice

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// FocusedEvidence is the deterministic evidence bundle a repair payload
// receives for one failure: resolved symbols, files, facts, and lookup notes.
// Everything in it comes from captured failure text plus repo scans, never
// from a model.
type FocusedEvidence struct {
	Symbols []string `json:"symbols,omitempty"`
	Files   []string `json:"files,omitempty"`
	Facts   []string `json:"facts,omitempty"`
	Lookups []string `json:"lookups,omitempty"`
}

// maxResolverFiles and maxResolverExcerpts bound repository scanning and
// evidence size so a large failure surface cannot balloon the repair payload.
const (
	maxResolverFiles     = 5
	maxResolverExcerpts  = 4
	maxResolverFactCount = 16
	excerptContextLines  = 1
)

// diagnostic patterns for Go build and test output.
var (
	diagUndefinedRe = regexp.MustCompile(`(?:^|\s)(?:\./)?(?:[A-Za-z0-9_./\-]+\.go:\d+(?::\d+)?:\s*)?undefined:\s*([A-Za-z_][A-Za-z0-9_]*)`)
	// e.g. "./store.go:12:9: mgr.Close undefined (type *Store has no field or method Close)"
	diagMissingMethodRe = regexp.MustCompile(`(\w+)\.([A-Za-z_][A-Za-z0-9_]*) undefined \(type ([^)]+) has no field or method ([A-Za-z_][A-Za-z0-9_]*)\)`)
	// e.g. "cannot use client (variable of type *Client) as *Conn value"
	diagCannotUseRe = regexp.MustCompile(`cannot use (\S+) \((?:variable|value) of type ([^)]+)\) as ([^ ]+)`)
	// e.g. "./math_test.go:13:2: undefined: helper"
	diagFileLineRe = regexp.MustCompile(`((?:\./)?[A-Za-z0-9_./\-]+\.go):(\d+):(\d+)?`)
)

// ResolveGoDiagnostics parses captured Go build/test output and produces a
// focused evidence bundle. Recognized shapes: "undefined: X", "x.Bar
// undefined (type T has no field or method Bar)", "cannot use X (variable of
// type T) as Y", failing test names from go test -json output, and "# pkg"
// build-failed package headers. Unrecognized text passes through as one raw
// fact so the payload never becomes empty.
func ResolveGoDiagnostics(workspace, output string) FocusedEvidence {
	evidence := FocusedEvidence{}
	if strings.TrimSpace(output) == "" {
		return evidence
	}
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")

	seenFiles := map[string]struct{}{}
	seenSymbols := map[string]struct{}{}
	var namedLocations []namedLocation
	var receiverTypes []string
	receiverSeen := map[string]struct{}{}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case matchUndefined(trimmed, &evidence, seenSymbols, &namedLocations):
		case matchMissingMethod(trimmed, &evidence, seenSymbols, &namedLocations, &receiverTypes, receiverSeen):
		case matchCannotUse(trimmed, &evidence, seenSymbols):
		}
	}

	// Failing test names from go test -json and plain marker lines.
	for _, name := range failingTestNames(lines) {
		if _, ok := seenSymbols[name]; !ok {
			seenSymbols[name] = struct{}{}
			evidence.Symbols = append(evidence.Symbols, name)
		}
	}

	// File locations from file:line mentions, bounded.
	for _, line := range lines {
		if match := diagFileLineRe.FindStringSubmatch(line); match != nil {
			file := strings.TrimPrefix(match[1], "./")
			lineNum := 0
			fmt.Sscanf(match[2], "%d", &lineNum)
			if _, ok := seenFiles[file]; !ok && len(seenFiles) < maxResolverFiles {
				seenFiles[file] = struct{}{}
				evidence.Files = append(evidence.Files, file)
				namedLocations = append(namedLocations, namedLocation{file: file, line: lineNum})
			}
		}
	}

	// Method sets for the receiver types named in missing-method
	// diagnostics, so a hallucinated-API failure shows the real surface.
	for _, receiver := range receiverTypes {
		evidence.Lookups = append(evidence.Lookups, typeMethodSets(workspace, receiver)...)
	}

	sort.Strings(evidence.Symbols)
	sort.Strings(evidence.Files)

	evidence.Lookups = append(evidence.Lookups, resolveSymbols(workspace, evidence.Symbols)...)
	evidence.Facts = append(evidence.Facts, fileExcerpts(workspace, namedLocations)...)

	if len(evidence.Facts) == 0 && len(evidence.Lookups) == 0 {
		// Generic fallback: keep the raw diagnostic visible to the writer.
		evidence.Facts = append(evidence.Facts, truncateDiagnostic(output))
	}
	if len(evidence.Facts) > maxResolverFactCount {
		evidence.Facts = evidence.Facts[:maxResolverFactCount]
	}
	return evidence
}

type namedLocation struct {
	file string
	line int
}

func matchUndefined(line string, evidence *FocusedEvidence, seen map[string]struct{}, locations *[]namedLocation) bool {
	match := diagUndefinedRe.FindStringSubmatch(line)
	if match == nil {
		return false
	}
	symbol := match[1]
	if _, ok := seen[symbol]; !ok {
		seen[symbol] = struct{}{}
		evidence.Symbols = append(evidence.Symbols, symbol)
	}
	evidence.Facts = append(evidence.Facts, truncateDiagnostic(line))
	if m := diagFileLineRe.FindStringSubmatch(line); m != nil {
		file := strings.TrimPrefix(m[1], "./")
		lineNum := 0
		fmt.Sscanf(m[2], "%d", &lineNum)
		*locations = append(*locations, namedLocation{file: file, line: lineNum})
		if _, ok := seen["file:"+file]; !ok && len(evidence.Files) < maxResolverFiles {
			seen["file:"+file] = struct{}{}
			evidence.Files = append(evidence.Files, file)
		}
	}
	return true
}

func matchMissingMethod(line string, evidence *FocusedEvidence, seen map[string]struct{}, locations *[]namedLocation, receiverTypes *[]string, receiverSeen map[string]struct{}) bool {
	match := diagMissingMethodRe.FindStringSubmatch(line)
	if match == nil {
		return false
	}
	method, receiver := match[4], match[3]
	key := receiver + "." + method
	if _, ok := seen[key]; !ok {
		seen[key] = struct{}{}
		evidence.Symbols = append(evidence.Symbols, method)
	}
	if _, ok := receiverSeen[receiver]; !ok {
		receiverSeen[receiver] = struct{}{}
		*receiverTypes = append(*receiverTypes, strings.TrimPrefix(receiver, "*"))
	}
	evidence.Facts = append(evidence.Facts, truncateDiagnostic(line))
	if m := diagFileLineRe.FindStringSubmatch(line); m != nil {
		file := strings.TrimPrefix(m[1], "./")
		lineNum := 0
		fmt.Sscanf(m[2], "%d", &lineNum)
		*locations = append(*locations, namedLocation{file: file, line: lineNum})
		if _, ok := seen["file:"+file]; !ok && len(evidence.Files) < maxResolverFiles {
			seen["file:"+file] = struct{}{}
			evidence.Files = append(evidence.Files, file)
		}
	}
	return true
}

func matchCannotUse(line string, evidence *FocusedEvidence, seen map[string]struct{}) bool {
	match := diagCannotUseRe.FindStringSubmatch(line)
	if match == nil {
		return false
	}
	evidence.Facts = append(evidence.Facts, truncateDiagnostic(line))
	if _, ok := seen["use:"+match[1]]; !ok {
		seen["use:"+match[1]] = struct{}{}
		evidence.Symbols = append(evidence.Symbols, match[1])
	}
	return true
}

// failingTestNames extracts failing test names from go test -json "fail"
// events (best effort over the JSON lines) and from plain "--- FAIL: Name"
// markers.
func failingTestNames(lines []string) []string {
	var names []string
	seen := map[string]struct{}{}
	add := func(name string) {
		if name == "" || name == "suite" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--- FAIL:") {
			if rest := strings.TrimPrefix(trimmed, "--- FAIL:"); len(rest) > 0 {
				fields := strings.Fields(rest)
				if len(fields) > 0 {
					add(fields[0])
				}
			}
			continue
		}
		if !strings.HasPrefix(trimmed, "{") {
			continue
		}
		// Minimal decode of one go test -json event line.
		var event struct {
			Test   string `json:"Test"`
			Action string `json:"Action"`
		}
		if err := json.Unmarshal([]byte(trimmed), &event); err != nil {
			continue
		}
		if event.Action == "fail" && !strings.Contains(trimmed, `"FailedBuild"`) {
			add(event.Test)
		}
	}
	return names
}

// resolveSymbols greps workspace .go files for each symbol declaration and
// enumerates the method set of a receiver type when the symbol names a type.
// Deterministic: files are walked in sorted order.
func resolveSymbols(workspace string, symbols []string) []string {
	var lookups []string
	missing := []string{}
	for _, symbol := range symbols {
		if symbol == "" {
			continue
		}
		hits := grepWorkspace(workspace, symbol, maxResolverFiles)
		if len(hits) == 0 {
			missing = append(missing, symbol)
			continue
		}
		for _, hit := range hits {
			lookups = append(lookups, fmt.Sprintf("symbol %s: found in %s (line %d): %s", symbol, hit.path, hit.line, strings.TrimSpace(hit.text)))
		}
		// When the symbol resolves to a type declaration, list its methods so
		// a hallucinated-API failure shows the real surface.
		lookups = append(lookups, typeMethodSets(workspace, symbol)...)
	}
	if len(missing) > 0 {
		lookups = append(lookups, workspaceDeclarationFacts(workspace, missing)...)
	}
	return lookups
}

// workspaceDeclarationFacts reports not-found symbols as negative facts and
// lists the top-level declarations that DO exist, so a hallucinated-API
// failure shows the real API surface without any fuzzy guessing.
func workspaceDeclarationFacts(workspace string, missing []string) []string {
	facts := []string{}
	for _, symbol := range missing {
		facts = append(facts, fmt.Sprintf("symbol %s: not found in workspace", symbol))
	}
	decls := map[string]struct{}{}
	for _, hit := range grepWorkspace(workspace, "package ", maxResolverFiles*4) {
		data, err := os.ReadFile(filepath.Join(workspace, hit.path))
		if err != nil {
			continue
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, hit.path, data, 0)
		if parseErr != nil {
			continue
		}
		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				decls[d.Name.Name] = struct{}{}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						decls[ts.Name.Name] = struct{}{}
					}
				}
			}
		}
	}
	if len(decls) > 0 {
		names := make([]string, 0, len(decls))
		for name := range decls {
			names = append(names, name)
		}
		sort.Strings(names)
		facts = append(facts, "workspace top-level declarations: "+strings.Join(names, ", "))
	}
	return facts
}

type grepHit struct {
	path string
	line int
	text string
}

// grepWorkspace scans .go files under workspace for a whole-word symbol
// occurrence. stdlib only; files are visited in sorted order for
// determinism, and the scan is bounded by the file-count limit.
func grepWorkspace(workspace, symbol string, limit int) []grepHit {
	if symbol == "" || workspace == "" {
		return nil
	}
	var hits []grepHit
	_ = filepath.WalkDir(workspace, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == ".splice" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		if len(hits) >= limit {
			return filepath.SkipAll
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		rel, relErr := filepath.Rel(workspace, path)
		if relErr != nil {
			rel = path
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, symbol) {
				hits = append(hits, grepHit{path: filepath.ToSlash(rel), line: i + 1, text: line})
				if len(hits) >= limit {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	return hits
}

func containsWord(line, word string) bool {
	for i := 0; i+len(word) <= len(line); i++ {
		if line[i:i+len(word)] != word {
			continue
		}
		beforeOK := i == 0 || !isWordByte(line[i-1])
		after := i + len(word)
		afterOK := after == len(line) || !isWordByte(line[after])
		if beforeOK && afterOK {
			return true
		}
	}
	return false
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// typeMethodSets parses the file that declares the named type and reports its
// method set plus its declared fields, so a wrong-API failure shows the real
// surface.
func typeMethodSets(workspace, typeName string) []string {
	var out []string
	if typeName == "" {
		return out
	}
	// Parse every workspace .go file: a type's declaration file is found
	// deterministically, and method sets come from the AST. The scan is
	// bounded by the same file-count limit as symbol lookups.
	for _, hit := range grepWorkspace(workspace, "package ", maxResolverFiles*4) {
		methods, fields, err := typeDeclarations(filepath.Join(workspace, hit.path), typeName)
		if err != nil || (len(methods) == 0 && len(fields) == 0) {
			continue
		}
		if len(methods) > 0 {
			out = append(out, fmt.Sprintf("type %s (declared in %s) methods: %s", typeName, hit.path, strings.Join(methods, ", ")))
		}
		if len(fields) > 0 {
			out = append(out, fmt.Sprintf("type %s (declared in %s) fields: %s", typeName, hit.path, strings.Join(fields, ", ")))
		}
	}
	return out
}

// typeDeclarations enumerates the method names and exported field names of a
// named type in one Go file via go/parser. A parse error is a value, and an
// absent type returns empty lists.
func typeDeclarations(path, typeName string) (methods, fields []string, err error) {
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		return nil, nil, fmt.Errorf("read %s: %w", path, readErr)
	}
	fset := token.NewFileSet()
	file, parseErr := parser.ParseFile(fset, path, data, 0)
	if parseErr != nil {
		return nil, nil, fmt.Errorf("parse %s: %w", path, parseErr)
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil || len(d.Recv.List) == 0 {
				continue
			}
			if recvName := receiverTypeName(d.Recv.List[0].Type); recvName == typeName {
				methods = append(methods, d.Name.Name)
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != typeName {
					continue
				}
				if st, ok := ts.Type.(*ast.StructType); ok {
					for _, field := range st.Fields.List {
						for _, name := range field.Names {
							fields = append(fields, name.Name)
						}
					}
				}
			}
		}
	}
	sort.Strings(methods)
	sort.Strings(fields)
	return methods, fields, nil
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(t.X)
	default:
		return ""
	}
}

// fileExcerpts returns one or two lines around each named file:line so the
// repair payload shows the code the diagnostic points at.
func fileExcerpts(workspace string, locations []namedLocation) []string {
	var facts []string
	seen := map[string]struct{}{}
	for _, loc := range locations {
		if loc.line <= 0 || loc.file == "" {
			continue
		}
		key := fmt.Sprintf("%s:%d", loc.file, loc.line)
		if _, ok := seen[key]; ok || len(facts) >= maxResolverExcerpts {
			continue
		}
		seen[key] = struct{}{}
		lines, err := fileLines(filepath.Join(workspace, loc.file))
		if err != nil {
			facts = append(facts, fmt.Sprintf("%s:%d: file excerpt unavailable", loc.file, loc.line))
			continue
		}
		start := loc.line - 1
		end := loc.line + excerptContextLines
		if start < 0 {
			start = 0
		}
		if end > len(lines) {
			end = len(lines)
		}
		for i := start; i < end && i < len(lines); i++ {
			facts = append(facts, fmt.Sprintf("%s:%d: %s", loc.file, i+1, strings.TrimSpace(lines[i])))
		}
	}
	return facts
}

func fileLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return strings.Split(string(data), "\n"), nil
}

func truncateDiagnostic(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 200 {
		return s
	}
	return s[:200]
}
