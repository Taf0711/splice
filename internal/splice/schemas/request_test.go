package schemas

import (
	"strings"
	"testing"
)

// TestBoundRequestKeepsTheRequestAsWritten pins audit item E03: a short
// request is returned byte for byte, with its whitespace and code fences.
func TestBoundRequestKeepsTheRequestAsWritten(t *testing.T) {
	request := "Fix this:\n\n```go\nfunc  Parse()  {}\n```\n"
	if got := BoundRequest(request); got != request {
		t.Fatalf("BoundRequest changed a short request:\n%q\nwant\n%q", got, request)
	}
}

// TestBoundRequestCapsAt32KiBWithMarker pins the 32 KiB bound: a longer
// request is cut, keeps its opening fence, and says so.
func TestBoundRequestCapsAt32KiBWithMarker(t *testing.T) {
	request := "Reproduce:\n```go\n" + strings.Repeat("x := 1 // filler line of code text\n", 1400) + "```\n"
	if len(request) <= MaxRequestBytes {
		t.Fatalf("fixture is %d bytes, want more than %d", len(request), MaxRequestBytes)
	}
	got := BoundRequest(request)
	if len(got) > MaxRequestBytes {
		t.Fatalf("bounded request is %d bytes, want at most %d", len(got), MaxRequestBytes)
	}
	if !strings.HasSuffix(got, "[request truncated at 32 KiB]") {
		t.Fatalf("cut request must end with the marker, got tail %q", got[len(got)-60:])
	}
	if !strings.HasPrefix(got, "Reproduce:\n```go\n") {
		t.Fatal("cut request must keep the opening fence and text unchanged")
	}
}

// TestBoundRequestClosesAnOpenFence pins audit item F9: a cut inside a code
// fence closes the fence before the marker, so the bounded text is well formed.
func TestBoundRequestClosesAnOpenFence(t *testing.T) {
	request := "intro\n```python\n" + strings.Repeat("x = 1  # a line of code\n", 2000) + "```\n"
	got := BoundRequest(request)
	if !strings.HasSuffix(got, "[request truncated at 32 KiB]") {
		t.Fatalf("cut request must end with the marker")
	}
	if strings.Count(got, "```")%2 != 0 {
		t.Fatal("the cut request leaves its code fence open")
	}
	if len(got) > MaxRequestBytes {
		t.Fatalf("bounded request is %d bytes, want at most %d", len(got), MaxRequestBytes)
	}
}
