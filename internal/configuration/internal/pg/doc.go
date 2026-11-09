// Package pg is the configuration domain's PostgreSQL storage: Stream, its
// migration stream, which creates no table yet.
//
// # Input and output
//
// None yet: the stream's migrations are embedded in the binary.
//
// # Dependencies
//
// It imports platform/sql/migrate. The configuration domain's assembly
// registers its stream.
package pg
