// Package pg is the access domain's PostgreSQL adapter: its migration
// stream and the stores that implement the core's ports. The stream holds
// decision 0007's tenancy (organizations, accounts, members, their
// identities and the members each account admits) and decision 0006's
// credentials (API keys, sessions, signing keys and each organization's
// break-glass credential). Store implements port.Tenancy.
//
// SealedColumns lists the stream's sealed columns, which internal/app
// joins with the platform's for resealing after a data key rotation.
//
// # Input and output
//
// PostgreSQL, through the pool or the transaction on the context.
//
// # Dependencies
//
// It imports the access domain's core packages and ports, internal/kernel,
// pgx, and platform/sql, platform/sql/migrate, platform/tx and
// platform/crypt for the column list. Only the access domain's assembly
// imports it.
package pg
