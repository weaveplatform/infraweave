// Package pg is the features domain's PostgreSQL storage: Stream, its
// migration stream of decision 0021's tiers, tier features and limit
// values, organization entitlements, the organization's feature and limit
// grants and account limits, which carry organization and account IDs
// without foreign keys because the domain sits below access; and Store,
// which creates and deletes an organization's and account's rows and reads
// an entitlement, under FOR UPDATE for a limit check. Every method joins
// the transaction its context carries.
//
// # Input and output
//
// It reads and writes PostgreSQL through the DB it is given.
//
// # Dependencies
//
// It imports featuresapi, internal/kernel, platform/sql,
// platform/sql/migrate, platform/tx and pgx. The features domain's
// assembly constructs it.
package pg
