// Package schema holds the platform stream: the migrations of the tables
// that belong to platform packages rather than to a domain, such as the
// shared idempotency keys and rate limits. Its tables may be referenced by
// every domain's and reference none, so internal/app registers it first
// (decisions 0004 and 0005).
//
// # Input and output
//
// Stream returns the embedded files as a migrate.Stream; it reads nothing
// at run time.
//
// # Dependencies
//
// It imports platform/sql/migrate for the Stream type. internal/app
// registers it.
package schema
