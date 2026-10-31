package main

// jobOwners names the team that answers for each scheduled job. The on-call
// rota pages this team when the job fails.
var jobOwners = map[string]string{
	"expire-sessions": "identity",
	"close-invoices":  "billing",
	"prune-audit-log": "security",
}
