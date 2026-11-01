// Package wiring checks that a declared feature seam has a production
// consumer. A green build and a green test suite cannot see a field that is
// produced and never read, a config that resolves and never reaches its
// consumer, or a callback that is defined and never invoked. This package
// makes each seam explicit and fails when the consumer reference disappears.
//
// The checker is static and uses only go/parser. It parses every non-test Go
// file in the module, records declarations and the names each production
// function references, and then checks a declarative contract list.
//
// A contract names a producer declaration and the production function or
// method that must reference it. A dropped reference fails the check. The
// contract also names the test that proves the end-to-end behavior, so a
// feature cannot be added without a proof.
package wiring

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Contract names one wiring seam.
//
// Producer is a declaration key: "dir.Func", "dir.Type", "dir.Type.Field", or
// "dir.Type.Method". The dir is the module-relative directory with forward
// slashes.
//
// Consumer is the production function or method that must reference the
// producer. It is the direct reader of the seam. If a refactor moves the read,
// update the consumer here; moving wiring must be deliberate.
//
// Proof is the module-relative test function that proves the end-to-end
// behavior, for example "internal/splice.TestScopedStageInputs".
//
// Inert marks a producer whose consumer is deliberately pending. An inert
// contract records Owner and Reason and skips the consumer check. The symbol
// must still exist, so the entry cannot rot.
type Contract struct {
	ID       string
	Producer string
	Consumer string
	Proof    string
	Inert    bool
	Owner    string
	Reason   string
}

// Violation reports one contract that failed.
type Violation struct {
	Contract string
	Message  string
}

// Error renders the violation.
func (v Violation) Error() string {
	return v.Contract + ": " + v.Message
}

// Check loads the module at root and returns every contract violation.
func Check(root string, contracts []Contract) ([]Violation, error) {
	index, err := indexRepo(root)
	if err != nil {
		return nil, err
	}
	return checkIndex(index, contracts), nil
}

// CheckWithTests is Check plus the test-function lookup, so a caller can assert
// that each contract proof exists.
func CheckWithTests(root string, contracts []Contract) ([]Violation, map[string]bool, error) {
	index, err := indexRepo(root)
	if err != nil {
		return nil, nil, err
	}
	return checkIndex(index, contracts), index.tests, nil
}

type repoIndex struct {
	decls     map[string]string
	consumers map[string]map[string]bool
	global    map[string]bool
	tests     map[string]bool
}

func checkIndex(index *repoIndex, contracts []Contract) []Violation {
	var violations []Violation
	for _, c := range contracts {
		if strings.TrimSpace(c.ID) == "" {
			violations = append(violations, Violation{Contract: "(unnamed)", Message: "contract has no id"})
			continue
		}
		if _, ok := index.decls[c.Producer]; !ok {
			violations = append(violations, Violation{Contract: c.ID, Message: "producer " + c.Producer + " is not declared"})
			continue
		}
		leaf := leafName(c.Producer)
		if !index.global[leaf] {
			violations = append(violations, Violation{Contract: c.ID, Message: "producer " + c.Producer + " is never referenced in production code"})
		}
		if c.Inert {
			if c.Owner == "" || c.Reason == "" {
				violations = append(violations, Violation{Contract: c.ID, Message: "inert contract needs an owner and a reason"})
			}
			continue
		}
		if c.Consumer == "" {
			violations = append(violations, Violation{Contract: c.ID, Message: "non-inert contract needs a consumer"})
			continue
		}
		refs, ok := index.consumers[c.Consumer]
		if !ok {
			violations = append(violations, Violation{Contract: c.ID, Message: "consumer " + c.Consumer + " is not a production function or method"})
			continue
		}
		if !refs[leaf] {
			violations = append(violations, Violation{Contract: c.ID, Message: "consumer " + c.Consumer + " does not reference producer " + c.Producer})
		}
	}
	return violations
}

func indexRepo(root string) (*repoIndex, error) {
	index := &repoIndex{
		decls:     map[string]string{},
		consumers: map[string]map[string]bool{},
		global:    map[string]bool{},
		tests:     map[string]bool{},
	}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			name := entry.Name()
			if name == "testdata" || name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			if path == filepath.Join(root, "memd") {
				// memd is a separate Go module with its own workflows.
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		dir := filepath.ToSlash(filepath.Dir(relative))
		if dir == "." {
			dir = ""
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", relative, parseErr)
		}
		if strings.HasSuffix(entry.Name(), "_test.go") {
			for _, decl := range file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok && strings.HasPrefix(fn.Name.Name, "Test") {
					index.tests[qualify(dir, fn.Name.Name)] = true
				}
			}
			return nil
		}
		index.indexProductionFile(dir, file)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return index, nil
}

func (index *repoIndex) indexProductionFile(dir string, file *ast.File) {
	for _, decl := range file.Decls {
		switch node := decl.(type) {
		case *ast.FuncDecl:
			key := funcKey(dir, node)
			index.decls[key] = key
			refs := collectRefs(node.Body)
			index.consumers[key] = refs
			for name := range refs {
				index.global[name] = true
			}
		case *ast.GenDecl:
			for _, spec := range node.Specs {
				switch typed := spec.(type) {
				case *ast.TypeSpec:
					index.decls[qualify(dir, typed.Name.Name)] = typed.Name.Name
					if structType, ok := typed.Type.(*ast.StructType); ok && structType.Fields != nil {
						for _, field := range structType.Fields.List {
							for _, name := range field.Names {
								index.decls[qualify(dir, typed.Name.Name+"."+name.Name)] = name.Name
							}
						}
					}
				case *ast.ValueSpec:
					for _, name := range typed.Names {
						index.decls[qualify(dir, name.Name)] = name.Name
					}
					for _, value := range typed.Values {
						for ref := range collectRefs(value) {
							index.global[ref] = true
						}
					}
				}
			}
		}
	}
}

// collectRefs returns every leaf name a node references. It counts selector
// reads (x.Field), composite-literal keys (Field: value), and bare identifiers
// (calls and type uses). Declaration names inside a function body also count,
// which is acceptable for a producer-existence check.
func collectRefs(node ast.Node) map[string]bool {
	refs := map[string]bool{}
	if node == nil {
		return refs
	}
	ast.Inspect(node, func(n ast.Node) bool {
		switch typed := n.(type) {
		case *ast.SelectorExpr:
			refs[typed.Sel.Name] = true
		case *ast.KeyValueExpr:
			if ident, ok := typed.Key.(*ast.Ident); ok {
				refs[ident.Name] = true
			}
		case *ast.Ident:
			refs[typed.Name] = true
		}
		return true
	})
	return refs
}

func funcKey(dir string, fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return qualify(dir, fn.Name.Name)
	}
	receiver := recvTypeName(fn.Recv.List[0].Type)
	return qualify(dir, receiver+"."+fn.Name.Name)
}

func recvTypeName(expr ast.Expr) string {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name
	case *ast.StarExpr:
		return recvTypeName(typed.X)
	case *ast.IndexExpr:
		return recvTypeName(typed.X)
	case *ast.IndexListExpr:
		return recvTypeName(typed.X)
	default:
		return ""
	}
}

func qualify(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "." + name
}

func leafName(key string) string {
	parts := strings.Split(key, ".")
	return parts[len(parts)-1]
}

// moduleRoot walks up from start until it finds a go.mod file.
func moduleRoot(start string) (string, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found above %s", start)
		}
		dir = parent
	}
}
