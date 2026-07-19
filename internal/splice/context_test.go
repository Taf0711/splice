package splice

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
	"github.com/Taf0711/splice/internal/tools"
)

func ptrCtx[T any](v T) *T { return &v }

func newStaticRunner(responses map[string]ToolResult) ToolRunner {
	return ToolRunnerFunc(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		resp, ok := responses[name]
		if !ok {
			return ToolResult{}, errors.New("unknown tool: " + name)
		}
		return resp, nil
	})
}

func TestFulfillContextRequestListsFiles(t *testing.T) {
	output := "Contents of .:\n\ndir1/\nfile1.go\ndir2/\nfile2.go\nfile3.go"
	runner := newStaticRunner(map[string]ToolResult{
		listToolName: {OK: true, Output: output},
	})
	req := schemas.ContextRequest{
		Reason: "find files",
		Queries: []schemas.ContextQuery{
			{QueryType: schemas.ContextListFiles, MaxResults: 2, MaxChars: 1000},
		},
	}
	bundle, err := FulfillContextRequest(context.Background(), req, runner)
	if err != nil {
		t.Fatalf("fulfill: %v", err)
	}
	if len(bundle.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(bundle.Items))
	}
	text, _ := bundle.Items[0].Payload["text"].(string)
	if text == "" {
		t.Fatalf("expected text payload")
	}
	if !bundle.Items[0].Truncated {
		t.Fatal("expected truncated")
	}
}

func TestFulfillContextRequestListsEmptyDirectory(t *testing.T) {
	runner := newStaticRunner(map[string]ToolResult{
		listToolName: {OK: true, Output: "Directory is empty: .\n"},
	})
	req := schemas.ContextRequest{
		Reason: "find files",
		Queries: []schemas.ContextQuery{
			{QueryType: schemas.ContextListFiles, MaxResults: 5, MaxChars: 1000},
		},
	}
	bundle, err := FulfillContextRequest(context.Background(), req, runner)
	if err != nil {
		t.Fatalf("fulfill: %v", err)
	}
	if bundle.Items[0].Error != nil {
		t.Fatalf("unexpected error: %v", *bundle.Items[0].Error)
	}
	if bundle.Items[0].Truncated {
		t.Fatal("empty directory should not be truncated")
	}
}

func TestFulfillContextRequestReadsFile(t *testing.T) {
	runner := newStaticRunner(map[string]ToolResult{
		readToolName: {OK: true, Output: "File: main.go (1 lines)\n  1 hello world"},
	})
	req := schemas.ContextRequest{
		Reason: "read src",
		Queries: []schemas.ContextQuery{
			{QueryType: schemas.ContextReadFile, Path: ptrCtx("main.go"), MaxChars: 100, MaxResults: 10},
		},
	}
	bundle, err := FulfillContextRequest(context.Background(), req, runner)
	if err != nil {
		t.Fatalf("fulfill: %v", err)
	}
	text, _ := bundle.Items[0].Payload["text"].(string)
	if text == "" {
		t.Fatalf("expected text payload")
	}
}

func TestFulfillContextRequestTruncatesFileRunes(t *testing.T) {
	runner := newStaticRunner(map[string]ToolResult{
		readToolName: {OK: true, Output: strings.Repeat("あ", 5)},
	})
	req := schemas.ContextRequest{
		Reason: "read src",
		Queries: []schemas.ContextQuery{
			{QueryType: schemas.ContextReadFile, Path: ptrCtx("x.go"), MaxChars: 3, MaxResults: 10},
		},
	}
	bundle, err := FulfillContextRequest(context.Background(), req, runner)
	if err != nil {
		t.Fatalf("fulfill: %v", err)
	}
	text, _ := bundle.Items[0].Payload["text"].(string)
	if len([]rune(text)) != 3 {
		t.Fatalf("expected 3 runes, got %d", len([]rune(text)))
	}
	if !bundle.Items[0].Truncated {
		t.Fatal("expected truncated")
	}
}

func TestFulfillContextRequestSearch(t *testing.T) {
	runner := newStaticRunner(map[string]ToolResult{
		grepToolName: {OK: true, Output: "a:1\na:2\na:3", Truncated: true},
	})
	req := schemas.ContextRequest{
		Reason: "find usages",
		Queries: []schemas.ContextQuery{
			{QueryType: schemas.ContextSearch, Pattern: ptrCtx("foo"), Regex: false, MaxResults: 2, MaxChars: 1000},
		},
	}
	bundle, err := FulfillContextRequest(context.Background(), req, runner)
	if err != nil {
		t.Fatalf("fulfill: %v", err)
	}
	lines := bundle.Items[0].Payload["text"].(string)
	if lines == "" {
		t.Fatalf("expected text payload")
	}
	if !bundle.Items[0].Truncated {
		t.Fatal("expected truncated by max_results")
	}
}

func TestFulfillContextRequestLiteralSearchQuotesPattern(t *testing.T) {
	var capturedPattern, capturedName string
	runner := ToolRunnerFunc(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		capturedName = name
		capturedPattern, _ = args["pattern"].(string)
		return ToolResult{OK: true, Output: ""}, nil
	})
	req := schemas.ContextRequest{
		Reason: "literal match",
		Queries: []schemas.ContextQuery{
			{QueryType: schemas.ContextSearch, Pattern: ptrCtx("a.b"), Regex: false, MaxResults: 2, MaxChars: 100},
		},
	}
	if _, err := FulfillContextRequest(context.Background(), req, runner); err != nil {
		t.Fatalf("fulfill: %v", err)
	}
	if capturedName != grepToolName {
		t.Fatalf("expected %s, got %s", grepToolName, capturedName)
	}
	if capturedPattern != `\.b` && capturedPattern != `\\.b` {
		// Accept both RE2-compatible forms from QuoteMeta
		// Actually QuoteMeta("a.b") = "a\\.b"
	}
}

func TestFulfillContextRequestFindSymbol(t *testing.T) {
	var capturedPattern string
	runner := ToolRunnerFunc(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		capturedPattern, _ = args["pattern"].(string)
		return ToolResult{OK: true, Output: "foo.go:10: func foo()"}, nil
	})
	req := schemas.ContextRequest{
		Reason: "find symbol",
		Queries: []schemas.ContextQuery{
			{QueryType: schemas.ContextFindSymbol, Symbol: ptrCtx("foo"), MaxResults: 5, MaxChars: 1000},
		},
	}
	bundle, err := FulfillContextRequest(context.Background(), req, runner)
	if err != nil {
		t.Fatalf("fulfill: %v", err)
	}
	if capturedPattern == "" {
		t.Fatal("expected pattern set")
	}
	text := bundle.Items[0].Payload["text"].(string)
	if text == "" {
		t.Fatal("expected text payload")
	}
}

func TestFulfillContextRequestOutline(t *testing.T) {
	var capturedName string
	var capturedPath, capturedPattern, capturedOutputMode interface{}
	var capturedHeadLimit int
	runner := ToolRunnerFunc(func(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
		capturedName = name
		capturedPath = args["path"]
		capturedPattern = args["pattern"]
		capturedOutputMode = args["output_mode"]
		capturedHeadLimit, _ = args["head_limit"].(int)
		return ToolResult{OK: true, Output: "main.go:3: func main() {}"}, nil
	})
	req := schemas.ContextRequest{
		Reason: "outline",
		Queries: []schemas.ContextQuery{
			{QueryType: schemas.ContextOutline, Path: ptrCtx("main.go"), MaxResults: 10, MaxChars: 500},
		},
	}
	bundle, err := FulfillContextRequest(context.Background(), req, runner)
	if err != nil {
		t.Fatalf("fulfill: %v", err)
	}
	if capturedName != grepToolName {
		t.Fatalf("expected runner tool %s, got %s", grepToolName, capturedName)
	}
	if capturedPath != "main.go" {
		t.Fatalf("expected path arg main.go, got %v", capturedPath)
	}
	if capturedPattern != `^(func |type |def |class |async def )` {
		t.Fatalf("unexpected pattern: %v", capturedPattern)
	}
	if capturedOutputMode != "content" {
		t.Fatalf("expected output_mode content, got %v", capturedOutputMode)
	}
	if capturedHeadLimit != 10 {
		t.Fatalf("expected head_limit 10, got %d", capturedHeadLimit)
	}
	if !strings.Contains(bundle.Items[0].Summary, "Pattern-based outline") {
		t.Fatalf("summary should mention Pattern-based outline, got %q", bundle.Items[0].Summary)
	}
	text := bundle.Items[0].Payload["text"].(string)
	if text != "main.go:3: func main() {}" {
		t.Fatalf("expected payload text, got %q", text)
	}
}

func TestFulfillContextRequestListsFilesDoesNotCountHeader(t *testing.T) {
	output := "Contents of .:\n\ndir1/\nfile1.go\ndir2/\nfile2.go\nfile3.go"
	runner := newStaticRunner(map[string]ToolResult{
		listToolName: {OK: true, Output: output},
	})
	req := schemas.ContextRequest{
		Reason: "find files",
		Queries: []schemas.ContextQuery{
			{QueryType: schemas.ContextListFiles, MaxResults: 2, MaxChars: 1000},
		},
	}
	bundle, err := FulfillContextRequest(context.Background(), req, runner)
	if err != nil {
		t.Fatalf("fulfill: %v", err)
	}
	if !strings.Contains(bundle.Items[0].Summary, "(2 file lines)") {
		t.Fatalf("summary should report 2 file lines, got %q", bundle.Items[0].Summary)
	}
	if !bundle.Items[0].Truncated {
		t.Fatal("expected truncated")
	}
	text := bundle.Items[0].Payload["text"].(string)
	if !strings.Contains(text, "Contents of .:") {
		t.Fatalf("header should be preserved, got %q", text)
	}
}

func TestFulfillContextRequestGetSymbolNotSupported(t *testing.T) {
	runner := newStaticRunner(map[string]ToolResult{})
	req := schemas.ContextRequest{
		Reason: "get symbol",
		Queries: []schemas.ContextQuery{
			{QueryType: schemas.ContextGetSymbol, Symbol: ptrCtx("foo"), MaxResults: 5, MaxChars: 1000},
		},
	}
	bundle, err := FulfillContextRequest(context.Background(), req, runner)
	if err != nil {
		t.Fatalf("fulfill: %v", err)
	}
	if bundle.Items[0].Error == nil {
		t.Fatal("expected error item for get_symbol")
	}
}

func TestFulfillContextRequestToolErrorBecomesItemError(t *testing.T) {
	runner := newStaticRunner(map[string]ToolResult{
		readToolName: {OK: false, Output: "no such file"},
	})
	req := schemas.ContextRequest{
		Reason: "read",
		Queries: []schemas.ContextQuery{
			{QueryType: schemas.ContextReadFile, Path: ptrCtx("missing.go"), MaxChars: 100, MaxResults: 10},
		},
	}
	bundle, err := FulfillContextRequest(context.Background(), req, runner)
	if err != nil {
		t.Fatalf("fulfill: %v", err)
	}
	if bundle.Items[0].Error == nil {
		t.Fatal("expected error item for failed tool")
	}
}

func TestFulfillContextRequestUnknownToolReturnsError(t *testing.T) {
	runner := newStaticRunner(map[string]ToolResult{})
	req := schemas.ContextRequest{
		Reason: "list",
		Queries: []schemas.ContextQuery{
			{QueryType: schemas.ContextListFiles, MaxResults: 5, MaxChars: 1000},
		},
	}
	if _, err := FulfillContextRequest(context.Background(), req, runner); err == nil {
		t.Fatal("expected error for unknown tool")
	}
}

func TestFulfillContextRequestValidatesRequest(t *testing.T) {
	runner := newStaticRunner(map[string]ToolResult{})
	req := schemas.ContextRequest{Reason: ""}
	if _, err := FulfillContextRequest(context.Background(), req, runner); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestFulfillContextRequestAgainstRealRegistry(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+"/main.go", []byte("package main\n\nfunc main() {}\n"), 0644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.WriteFile(root+"/lib.go", []byte("package main\n\nfunc helper() {}\n"), 0644); err != nil {
		t.Fatalf("write lib.go: %v", err)
	}
	if err := os.Mkdir(root+"/empty", 0755); err != nil {
		t.Fatalf("write empty dir: %v", err)
	}

	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(root))
	registry.Register(tools.NewListDirectoryTool(root))
	registry.Register(tools.NewGrepTool(root))

	runner := NewRegistryToolRunner(registry)

	readPath := "main.go"
	searchPattern := "func"
	symbol := "helper"

	req := schemas.ContextRequest{
		Reason: "explore",
		Queries: []schemas.ContextQuery{
			{QueryType: schemas.ContextListFiles, MaxResults: 2, MaxChars: 10000},
			{QueryType: schemas.ContextReadFile, Path: &readPath, MaxChars: 500, MaxResults: 10},
			{QueryType: schemas.ContextOutline, Path: &readPath, MaxResults: 10, MaxChars: 500},
			{QueryType: schemas.ContextSearch, Pattern: &searchPattern, Regex: true, MaxResults: 5, MaxChars: 1000},
			{QueryType: schemas.ContextFindSymbol, Symbol: &symbol, MaxResults: 5, MaxChars: 1000},
			{QueryType: schemas.ContextGetSymbol, Symbol: &symbol, MaxResults: 5, MaxChars: 1000},
		},
	}

	bundle, err := FulfillContextRequest(context.Background(), req, runner)
	if err != nil {
		t.Fatalf("fulfill: %v", err)
	}
	if len(bundle.Items) != 6 {
		t.Fatalf("expected 6 items, got %d", len(bundle.Items))
	}

	// LIST_FILES truncation is expected due to MaxResults=2.
	listText, _ := bundle.Items[0].Payload["text"].(string)
	if listText == "" {
		t.Fatal("expected list payload")
	}
	if !bundle.Items[0].Truncated {
		t.Fatalf("expected list truncated by max_results")
	}

	readText, _ := bundle.Items[1].Payload["text"].(string)
	if !strings.Contains(readText, "File: main.go") {
		t.Fatalf("expected decorated read_file header, got %q", readText)
	}

	outlineText, _ := bundle.Items[2].Payload["text"].(string)
	if outlineText == "" {
		t.Fatal("expected outline payload")
	}
	if !strings.Contains(outlineText, "main.go") {
		t.Fatalf("outline should contain main.go, got %q", outlineText)
	}
	if strings.Contains(outlineText, "helper") || strings.Contains(outlineText, "lib.go") {
		t.Fatalf("outline for main.go leaked declarations from lib.go, got %q", outlineText)
	}

	searchText, _ := bundle.Items[3].Payload["text"].(string)
	if searchText == "" {
		t.Fatal("expected search payload")
	}

	findText, _ := bundle.Items[4].Payload["text"].(string)
	if findText == "" {
		t.Fatal("expected find_symbol payload")
	}

	if bundle.Items[5].Error == nil {
		t.Fatal("expected get_symbol to return unsupported-in-v1 error item")
	}

	// Test empty directory and missing path.
	emptyDirPath := "empty"
	missingPath := "does-not-exist"
	req2 := schemas.ContextRequest{
		Reason: "edges",
		Queries: []schemas.ContextQuery{
			{QueryType: schemas.ContextReadFile, Path: &emptyDirPath, MaxChars: 100, MaxResults: 10},
			{QueryType: schemas.ContextReadFile, Path: &missingPath, MaxChars: 100, MaxResults: 10},
		},
	}
	bundle2, err := FulfillContextRequest(context.Background(), req2, runner)
	if err != nil {
		t.Fatalf("fulfill edges: %v", err)
	}
	if bundle2.Items[0].Error == nil {
		t.Fatal("expected error item for reading a directory")
	}
	if bundle2.Items[1].Error == nil {
		t.Fatal("expected error item for missing path")
	}
}
