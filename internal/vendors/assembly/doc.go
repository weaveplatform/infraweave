// Package assembly constructs the vendors domain, the tool version
// catalogue, after access and ahead of stacks. Stream is its migration
// stream; Settings name the release index each vendor's versions are read
// from, the vendors' public ones by default; Cron is its periodic task,
// Refresh, which reads the indexes into the catalogue every RefreshEvery,
// once across every replica; Catalog is vendorsapi.Catalog; and API is
// its partial of the API's strict server, aliased VendorsAPI.
//
// ref: decisions 0004 and 0011
//
// # Input and output
//
// Its refresh sends GET requests to the configured indexes, each bounded
// by a minute, and writes the releases they list to PostgreSQL.
//
// # Dependencies
//
// It imports vendorsapi, the domain's http, index, pg and vendors
// packages, internal/iac, platform/cron, platform/queue,
// platform/sql/migrate and platform/tx. Only internal/app and cmd/ import
// it, as the constructors rule enforces.
package assembly
