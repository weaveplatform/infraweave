// Package reviewmeta supplies immutable review metadata from access-owned rows.
//
// ref: decision 0007
// ref: decision 0009
//
// # Input and output
//
// ReviewMetadata accepts an authenticated principal and account-owned space.
// It returns the admitted member identity, current role definitions and actual
// session/request facts. Human sessions and personal keys share one author key.
// CanReview decides RUN_REVIEW from fresh owner data after the route guard.
//
// # Effects
//
// Reads join the caller's serializable transaction. No credential, token or
// secret leaves the access owner; the service neither records a review nor
// changes authorization rows.
//
// # Dependencies
//
// Access APIs and its authorization, role and session models, kernel and the
// transaction platform. The access PostgreSQL adapter implements Store.
package reviewmeta
