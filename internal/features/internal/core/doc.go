// Package core is the features domain's logic of tiers, entitlements
// and limits: Service implements featuresapi.Entitlements over a Store. An
// organization's entitlement is its tier's features and its granted
// features, less the installation's features.disabled, less every feature
// whose requirements that leaves out, with each limit's value from a
// grant, else the tier; with the billing switch off it reads as
// Enterprise. Allow, for a request's principal, and AllowAccount, for an
// option check or a job, refuse a feature that is switched off; with
// billing on, one the organization lacks, naming the tiers that hold it;
// and, billing on or off, a feature in preview the account has not opted
// in to. The features in preview are the compiled catalogue's unless
// WithPreviews names others. Each account's opt-in to feature previews,
// off by default, and each member's personal setting, on by default, are
// read and set through the Service; the personal setting refuses nothing,
// and FeaturePreviews lists the previews the installation has not
// switched off with whether each is Active for a principal.
// SetAccountFeaturePreviews and SetPersonalFeaturePreviews write a setting
// and answer that list with the write applied; the personal one refuses a
// principal that names no member with ErrNoMember. CheckLimit locks the organization's entitlement, counts the
// limit's use through the caller, and refuses one more beyond the
// organization's pool or the account's limit, pool first; Quota reads the
// value of a limit it applies to an account. The replica
// keeps a snapshot of each entitlement, each account's opt-in, the switch
// and the tiers, which Invalidate and Flush drop. The operator's edits run through Service too:
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
// None of its own: it reaches storage through Store, with PreviewStore
// for the feature preview settings, and transactions through Transactor.
//
// # Dependencies
//
// It imports the catalogue (internal/features), featuresapi and
// internal/kernel. The features domain's assembly constructs it.
package core
