// Package features is the features domain's logic of decision 0021:
// Service implements featuresapi.Entitlements over a Store. An
// organization's entitlement is its tier's features and its granted
// features, less the installation's features.disabled, with each limit's
// value from a grant, else the tier. CheckLimit locks the organization's
// entitlement, counts the limit's use through the caller, and refuses one
// more beyond the organization's pool or the account's limit, pool first.
//
// # Input and output
//
// None of its own: it reaches storage through Store and transactions
// through Transactor.
//
// # Dependencies
//
// It imports featuresapi and internal/kernel. The features domain's
// assembly constructs it.
package features
