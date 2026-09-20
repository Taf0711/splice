#!/bin/bash
# Gold solution for large-01 Task A: per-account dunning levels on Service.
set -e
cd "$1"
mkdir -p internal/billing
cat > internal/billing/notices.go <<'GOEOF'
package billing

// RecordDunningNotice advances and returns the account dunning level,
// keeping per-account levels in the service.
func (b *Service) RecordDunningNotice(accountID string) DunningLevel {
	if b.dunningLevels == nil {
		b.dunningLevels = map[string]DunningLevel{}
	}
	b.dunningLevels[accountID] = b.dunningLevels[accountID].Next()
	return b.dunningLevels[accountID]
}
GOEOF
python3 - <<'PYEOF'
import re
p = "internal/billing/invoice.go"
src = open(p).read()
pattern = re.compile(
    r"type Service struct \{
\s*invoices map\[string\]Invoice
\}",
)
assert pattern.search(src), "Service struct not found"
src = pattern.sub(
    "type Service struct {
\tinvoices map[string]Invoice
\tdunningLevels map[string]DunningLevel
}",
    src,
)
open(p, "w").write(src)
print("patched")
PYEOF
gofmt -w internal/billing/invoice.go
