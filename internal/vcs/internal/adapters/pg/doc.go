// Package pg is the vcs domain's PostgreSQL storage: Store, the
// integrations, which seals each one's credential and webhook secret to
// its row and opens them only for Secrets, refuses a second default of a
// provider through vcs_integrations_default, searches them by the
// integration package's Fields, and purges a deleted account's a batch at
// a time; Stream, the migration stream that creates them; Held, the
// columns that name other domains' rows; and SealedColumns, the three
// sealed secret columns.
//
// # Input and output
//
// PostgreSQL, through the pool its caller passes; the stream's migrations
// are embedded in the binary.
//
// # Dependencies
//
// It imports the domain's integration package, internal/kernel and its
// search package, platform/crypt, platform/sql and its search, integrity
// and migrate packages, and platform/tx. The domain's assembly constructs
// it and internal/app registers its stream.
package pg
