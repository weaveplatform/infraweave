// Package pg is the access domain's PostgreSQL adapter: its migration
// stream and the stores that implement the core's ports. The stream holds
// decision 0007's tenancy (organizations, accounts, members, their
// identities and the members each account admits) and decision 0006's
// credentials (API keys, sessions, signing keys and each organization's
// break-glass credential), with migration 00003 adding each account's
// api_key_non_admin_managers. Store implements port.Tenancy,
// port.Sessions, port.APIKeys, apikey.Store and operator.Store, with an
// OIDC key's claim mapping as jsonb. operator.Store's writes record their
// organization trail entry through platform/audit in the same
// transaction; deleting an organization revokes its live sessions, then
// deletes its sessions, API keys, members and their identities, its
// break-glass credential, its accounts, its rows in the features domain
// through the featuresapi.Entitlements WithFeatures gives, and itself, as
// every foreign key restricts. A key's first use in SeatWindow takes it a
// seat, checked against the seats limit through the same Entitlements in
// the transaction that records the use, counting each active member once
// and each other live key that is not personal and was used in the window.
// A unique violation on a slug is
// port.ErrSlugTaken and a
// break-glass credential of no organization port.ErrNotFound. Store also
// implements port.BreakGlass, whose sign-in writes the session and its
// trail entry in one transaction, and session.Admission, from
// account_users. Every revocation sends session:<id> on
// infraweave_invalidate in its own transaction, and a member's new
// session in a channel revokes their live one there. SigningKeys
// implements token.Store: under the local backend it generates each key
// and seals its private half into signing_keys.private_key_sealed bound
// to the key's id; under the kms backend it stores a KMS key's reference
// in kms_key_ref and signs through the opened KMS key of that reference
// (ErrKMSRef when none is). It applies a rotation under a SHARE ROW
// EXCLUSIVE lock, so replicas rotating together, the first key's creation
// among them, take turns while verifiers keep reading.
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
// It imports the access domain's core packages and ports, featuresapi,
// internal/kernel,
// pgx, and platform/sql, platform/sql/migrate, platform/sql/notify,
// platform/tx, platform/audit for the organization trail,
// platform/crypt for the sealed column and platform/crypt/signer for the
// keys. Only the access domain's assembly
// imports it.
package pg
