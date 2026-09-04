package splice

import (
	"testing"

	"github.com/Taf0711/splice/internal/memd"
)

// TestGraphMemoryStoreSatisfiesMemoryStore pins the adapter contract: the
// graph-enabled MemoryStore must satisfy the pipeline interface and expose
// the sidecar client for the run seam's capture path.
func TestGraphMemoryStoreSatisfiesMemoryStore(t *testing.T) {
	var store MemoryStore = graphMemoryStore{}
	g, ok := store.(interface{ GraphClient() *memd.Client })
	if !ok {
		t.Fatal("graphMemoryStore must expose GraphClient for the capture path")
	}
	if g.GraphClient() != nil {
		t.Fatal("zero-value wrapper must expose a nil client (capture skipped)")
	}
}

// TestNewGraphMemoryStoreWraps pins that NewGraphMemoryStore returns a
// MemoryStore whose GraphClient returns the wrapped client.
func TestNewGraphMemoryStoreWrapsClient(t *testing.T) {
	// nil client: still a MemoryStore, GraphClient() reports nil so capture
	// degrades to cold rather than panicking.
	store := NewGraphMemoryStore(nil)
	g, ok := store.(interface{ GraphClient() *memd.Client })
	if !ok {
		t.Fatal("NewGraphMemoryStore must produce a GraphClient provider")
	}
	if c := g.GraphClient(); c != nil {
		t.Fatalf("GraphClient = %v, want nil for nil input", c)
	}
}