// Package features is the features domain's logic of decision 0021:
// Service implements featuresapi.Entitlements over a Store. An
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
// Invalidate and Flush drop.
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
