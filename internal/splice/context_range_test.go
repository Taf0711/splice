package splice

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// rangeWorkspace writes a 100-line file whose lines carry unique markers.
func rangeWorkspace(t *testing.T) (ToolRunner, string) {
	t.Helper()
	workDir, registry := newRunTestWorkspace(t)
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&b, "MARK-%03d\n", i)
	}
	if err := os.WriteFile(filepath.Join(workDir, "big.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return NewRegistryToolRunner(registry), "big.txt"
}

func readQuery(path string, start, end *int) schemas.ContextQuery {
	p := path
	return schemas.ContextQuery{QueryType: schemas.ContextReadFile, Path: &p, StartLine: start, EndLine: end, MaxResults: 10, MaxChars: 4000}
}

func intp(v int) *int { return &v }

// TestRangedReadDeliversOnlyTheRange pins audit item E04: a ranged read
// returns the lines in range, and its start and end describe that range.
func TestRangedReadDeliversOnlyTheRange(t *testing.T) {
	runner, path := rangeWorkspace(t)
	item, err := fulfillReadFile(context.Background(), readQuery(path, intp(40), intp(42)), runner)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := item.Payload["text"].(string)
	for _, want := range []string{"MARK-040", "MARK-042"} {
		if !strings.Contains(text, want) {
			t.Fatalf("ranged text lacks %s:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"MARK-001", "MARK-039", "MARK-043"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("ranged text carries %s outside the range:\n%s", unwanted, text)
		}
	}
	if item.Payload["start"] != 40 || item.Payload["end"] != 42 {
		t.Fatalf("payload range = %v-%v, want 40-42", item.Payload["start"], item.Payload["end"])
	}
}

// TestRangedReadClampsToTheFile pins that an out-of-range end is clamped to
// the file's last line, and the payload describes the clamped range.
func TestRangedReadClampsToTheFile(t *testing.T) {
	runner, path := rangeWorkspace(t)
	item, err := fulfillReadFile(context.Background(), readQuery(path, intp(95), intp(500)), runner)
	if err != nil {
		t.Fatal(err)
	}
	text, _ := item.Payload["text"].(string)
	if !strings.Contains(text, "MARK-100") || strings.Contains(text, "MARK-094") {
		t.Fatalf("clamped text must hold lines 95 to the end:\n%s", text)
	}
	if item.Payload["start"] != 95 {
		t.Fatalf("payload start = %v, want 95", item.Payload["start"])
	}
	if item.Payload["end"] != rawLineCount([]byte(strings.Repeat("x\n", 100))) {
		t.Fatalf("payload end = %v, want the clamped last line", item.Payload["end"])
	}
}

// TestTwoRangesOfOneFileBothCountAsNewBytes pins the expansion ledger: two
// different ranges of one file are two deliveries, so the second is new bytes.
func TestTwoRangesOfOneFileBothCountAsNewBytes(t *testing.T) {
	runner, path := rangeWorkspace(t)
	first, err := fulfillReadFile(context.Background(), readQuery(path, intp(1), intp(2)), runner)
	if err != nil {
		t.Fatal(err)
	}
	second, err := fulfillReadFile(context.Background(), readQuery(path, intp(40), intp(42)), runner)
	if err != nil {
		t.Fatal(err)
	}
	ledger := newExpansionLedger()
	ledger.RecordDigests(&schemas.ContextBundle{Items: []schemas.ContextItem{first}})
	if !ledger.NewBytes(&schemas.ContextBundle{Items: []schemas.ContextItem{second}}) {
		t.Fatal("a different range of an already delivered file must count as new bytes")
	}
}

// TestWholeFileReadIsUnchanged pins that a query without a range keeps the
// whole-file payload: start 1, end at the last line, and the full text.
func TestWholeFileReadIsUnchanged(t *testing.T) {
	runner, path := rangeWorkspace(t)
	item, err := fulfillReadFile(context.Background(), readQuery(path, nil, nil), runner)
	if err != nil {
		t.Fatal(err)
	}
	if item.Payload["start"] != 1 || item.Payload["end"] != rawLineCount([]byte(strings.Repeat("x\n", 100))) {
		t.Fatalf("whole-file payload range = %v-%v", item.Payload["start"], item.Payload["end"])
	}
	text, _ := item.Payload["text"].(string)
	if !strings.Contains(text, "MARK-001") || !strings.Contains(text, "MARK-100") {
		t.Fatalf("whole-file text must hold the file:\n%s", text)
	}
}
