// Package pg is the vendors domain's PostgreSQL storage: Stream, its
// migration stream, whose vendor_releases table holds each vendor's
// releases by version, with when the index said each was published, the
// same for every account; and Store, which adds the releases it does not
// hold, never rewriting one it holds, and lists a vendor's versions.
// Every method joins the transaction its context carries.
//
// # Input and output
//
// It reads and writes PostgreSQL through the querier it is given.
//
// # Dependencies
//
// It imports internal/iac for the vendor kinds, the domain's index
// package for its releases, platform/sql, platform/sql/migrate,
// platform/tx and pgx. The vendors domain's assembly constructs it.
package pg
