package splice

import (
	"context"

	"github.com/Taf0711/splice/internal/memd"
	"github.com/Taf0711/splice/internal/splice/schemas"
)

// graphMemoryStore wraps a *memd.Client so it satisfies MemoryStore AND
// exposes the graph client to the run seam via GraphClient(). The run's
// verified-run capture keys on that interface, so wrapping is what turns
// graph capture on for real runs; a plain memd.Client passed as
// MemoryStore still works for retrieval but skips capture.
type graphMemoryStore struct {
	*memd.Client
}

// GraphClient exposes the sidecar client for the run seam's capture path.
func (g graphMemoryStore) GraphClient() *memd.Client { return g.Client }

var _ MemoryStore = graphMemoryStore{}

// NewGraphMemoryStore adapts a sidecar client into the pipeline MemoryStore
// with graph capture enabled. Callers passing a bare *memd.Client as
// MemoryStore get retrieval only; this wrapper also enables the
// verified-run capture path.
func NewGraphMemoryStore(client *memd.Client) MemoryStore {
	return graphMemoryStore{Client: client}
}

var _ = context.Background
var _ = schemas.MemoryQuery{}
