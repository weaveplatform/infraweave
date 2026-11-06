// Package audit is the write path of decision 0015's audit trail: every
// domain records an entry of the account's trail in the transaction of the
// change it describes, so an entry exists exactly when the change does.
//
// Record takes a kernel.AuditEntry, refuses one without its account,
// actor, action, event type or affected resource (ErrEntry) or made
// outside a transaction (ErrNoTransaction), assigns its ID, and inserts it.
// RecordOrganization does the same for a kernel.OrganizationAuditEntry,
// the organization's own trail of changes with no account. The trails'
// search, retention, write-only field sanitizing and ordered shipping are
// built on the same tables by the events domain.
//
// # Input and output
//
// It writes the platform stream's audit_trail_entries and
// organization_audit_trail_entries tables through the
// transaction on the context and reads nothing. Body and resource metadata
// are stored as the JSON given. Database errors that mean PostgreSQL is
// unreachable are marked with kernel.ErrUnavailable. It logs nothing.
//
// # Dependencies
//
// It imports platform/tx, platform/sql for the unavailable error, and
// internal/kernel for AuditEntry and ULIDs. Every domain's stores call it.
package audit
