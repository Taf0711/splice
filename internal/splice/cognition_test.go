package splice

import (
	"testing"

	"github.com/Taf0711/splice/internal/memd"
)

// TestMemdClientImplementsTopicLookupStore is the pairing pin for the C0.2
// capability: the real sidecar client must satisfy TopicLookupStore so the
// fast path is reachable in production, not only through test fakes. A
// compile-time assertion would work too; this keeps the dependency explicit
// and greppable. internal/memd imports only schemas, so there is no cycle.
func TestMemdClientImplementsTopicLookupStore(t *testing.T) {
	var _ TopicLookupStore = (*memd.Client)(nil)
}
