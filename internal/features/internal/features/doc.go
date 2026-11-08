// Package features is the features domain's logic of tiers, entitlements
// and limits: Service implements featuresapi.Entitlements over a Store. An
// organization's entitlement is its tier's features and its granted
// features, less the installation's features.disabled, less every feature
// whose requirements that leaves out, with each limit's value from a
// grant, else the tier; with the billing switch off it reads as
// Enterprise. Allow refuses a feature that is switched off, or, with
// billing on, that the caller's organization lacks, naming the tiers that
// hold it. CheckLimit locks the organization's entitlement, counts the
// limit's use through the caller, and refuses one more beyond the
// organization's pool or the account's limit, pool first. The replica
// keeps a snapshot of each entitlement, the switch and the tiers, which
// Invalidate and Flush drop. The operator's edits run through Service too:
// CheckTier holds a tier to the catalogue (gated features, each once, and
// limits of zero or more), Enterprise cannot be edited or deleted
// (ErrInvalidState), and OrganizationEntitlement is what an organization's
// tier and grants entitle it to while billing is on, with its grants.
// The account-facing reads, which in the reference report the vendor's own
// billing, answer from the installation's data instead:
// OrganizationTier is the tier an organization is on as the operator
// defines it, Enterprise while billing is off; Entitled is its
// entitlement's features; Seats is its pooled seats limit and an
// account's own, with the use the caller counted, and no limit while
// billing is off.
//
// ref: decision 0021
//
// # Input and output
//
// None of its own: it reaches storage through Store and transactions
// through Transactor.
//
// # Dependencies
//
// It imports the catalogue (internal/features), featuresapi and
// internal/kernel. The features domain's assembly constructs it.
package features
