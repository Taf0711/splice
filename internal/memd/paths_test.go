package memd

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Taf0711/splice/internal/splice/schemas"
)

// TestCanonicalProjectPathResolvesSymlinks pins the identity rule: a
// directory reachable through two spellings resolves to one. This is the
// /var vs /private/var split that made an imported capture invisible to
// retrieval keyed from the resolved form (the fam-05 deepseek run).
func TestCanonicalProjectPathResolvesSymlinks(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if CanonicalProjectPath(link) != CanonicalProjectPath(real) {
		t.Fatalf("spellings disagree: %q vs %q", CanonicalProjectPath(link), CanonicalProjectPath(real))
	}
	if got := CanonicalProjectPath(""); got != "" {
		t.Fatalf("empty path changed: %q", got)
	}
}

// TestClientProjectIdentityIsSpellingAgnostic walks the live chain with a
// real client: an observation upsert, a topic lookup, and an exact-anchor
// graph query are issued through one spelling of the project directory, and
// every outgoing payload must carry the canonical spelling. Any project-path
// carrier that forgets to canonicalize fails here. Retrieval keyed from the
// other spelling is covered by the identity rule above plus the splice-side
// discovery pairing test.
func TestClientProjectIdentityIsSpellingAgnostic(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	var mu sync.Mutex
	payloads := map[string]string{}
	respond := map[string]string{
		"/upsert":       `{"ok":true,"observation":{"id":1}}`,
		"/lookup_topic": `{"ok":true,"observations":[]}`,
		"/graph/exact":  `{"ok":true,"nodes":[]}`,
	}
	c := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1<<16)
		n, _ := r.Body.Read(buf)
		mu.Lock()
		payloads[r.URL.Path] = string(buf[:n])
		mu.Unlock()
		if body, ok := respond[r.URL.Path]; ok {
			w.Write([]byte(body))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))

	// Upsert through the link spelling.
	obs := validObservation()
	obs.ProjectPath = ptr(link)
	obs.Scope = "project"
	obs.Visibility = "shareable"
	if _, err := c.Upsert(t.Context(), obs); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// Topic lookup through the link spelling.
	if _, err := c.LookupTopic(t.Context(), schemas.MemoryTopicQuery{
		ProjectPath:     link,
		RequestingAgent: "code_writer",
		Scope:           "project",
		TopicKey:        "file:main.go",
		Limit:           8,
	}); err != nil {
		t.Fatalf("lookup topic: %v", err)
	}

	// Exact-anchor graph query through the link spelling.
	if _, err := c.GetExactNodes(t.Context(), map[string][]string{"file": {"main.go"}}, link, 4); err != nil {
		t.Fatalf("exact nodes: %v", err)
	}

	canonical := CanonicalProjectPath(real)
	for path, body := range payloads {
		if !strings.Contains(body, canonical) {
			t.Fatalf("%s payload does not carry the canonical project path:\n%s", path, body)
		}
		if strings.Contains(body, `"`+link+`"`) {
			t.Fatalf("%s payload carries the symlink spelling:\n%s", path, body)
		}
	}
	if len(payloads) != 3 {
		t.Fatalf("expected 3 captured payloads, got %d (%v)", len(payloads), keysOf(payloads))
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
