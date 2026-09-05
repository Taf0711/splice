package admin

import "demo/internal/audit"

// AuditBridge records admin actions into the audit trail.
type AuditBridge struct {
    trail *audit.Trail
}

// NewAuditBridge wires the admin package to the audit trail.
func NewAuditBridge(trail *audit.Trail) *AuditBridge {
    return &AuditBridge{trail: trail}
}

// Record writes one admin action.
func (b *AuditBridge) Record(actor, action, object string) {
    b.trail.Record(actor, action, object, "ok")
}
