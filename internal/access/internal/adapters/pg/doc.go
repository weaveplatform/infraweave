// Package pg is the access domain's PostgreSQL adapter: its migration
// stream and the stores that implement the core's ports. The stream holds
// decision 0007's tenancy (organizations, accounts, members, their
// identities and the members each account admits) and decision 0006's
// credentials (API keys, sessions, signing keys and each organization's
// break-glass credential), with migration 00003 adding each account's
// api_key_non_admin_managers, 00004 its account_settings, a row
// created on the first write, so an account without one reads each
// column's default, and 00005 the Enterprise entitlement of every
// organization without one, where the features stream is applied, and
// 00006 organization_admins, the members holding organization-admin, and
// organization_keys, and 00007 the pending member, each admission's
// latest_groups and slack_member_id, account_invitations,
// managed_user_groups and a personal key's owner as a member of the
// organization rather than an admission, so removing a managed user
// keeps the keys it disables, and 00009 spaces and space_closure, with the
// top space and legacy of every account that exists before it, and 00010
// roles, role_actions and role_bindings, with the system roles of every
// account that exists before it. Creating an account, with its
// organization or by an organization admin, seeds its top space, legacy
// and system roles in the same transaction, and claiming or creating one
// binds space-admin on its top space to the account admin it names. A
// key's Admin reads whether it holds that binding.
// Creating an organization creates its entitlement through
// featuresapi.Entitlements in the same transaction. Store implements port.Tenancy, port.Sessions,
// port.APIKeys, port.OrganizationKeys, apikey.Store, operator.Store,
// orgadmin.Store, settings.Store, space.Store, user.Store and group.Store, with an
// OIDC key's claim mapping as jsonb. operator.Store's writes record their
// organization trail entry through platform/audit in the same
// transaction; deleting an organization revokes its live sessions, then
// deletes its sessions, API keys, organization keys and admins,
// invitations, managed user groups, members
// and their identities, its
// break-glass credential, its accounts' settings, role bindings, roles and
// space trees, its accounts, its rows in the features domain
// through the featuresapi.Entitlements WithFeatures gives, and itself, as
// every foreign key restricts. Deleting an account takes its
// organization's row lock, so two deletions never both remove an
// organization's last two accounts, then deletes the account's sessions,
// API keys, invitations, managed user groups, admissions, settings,
// role bindings, roles, space tree and features rows and the account, and
// records its organization trail entry in the same transaction; turning
// personal keys off with disableExistingKeys disables them for ACCOUNT
// and revokes their sessions. orgadmin.Store's writes record their
// organization trail entry in the same transaction: claiming an account
// locks it, and admitting an account admin checks the seat it takes,
// as a key's first use does; deactivating a member disables their
// personal keys as DEPROVISIONED and revokes their and those keys'
// sessions; disabling or deleting an organization key revokes its
// sessions. user.Store's writes record their account trail entry in the
// same transaction: inviting a login takes its organization's row lock,
// admits its member or a new pending one and checks the seat a member no
// other account admits takes; removing a managed user deletes its
// invitations, disables its personal keys in the account as
// DEPROVISIONED and revokes their and its sessions there. A group's
// members are the admissions whose latest_groups hold its name. Creating
// a personal key share-locks the account, the owner's admission and the
// member while it reads personalApiKeysEnabled and the owner's state,
// and turning the setting off, removing the member and deactivating them
// lock the same rows first, so none misses a key created beside it;
// PersonalCreatable reads the same rule without the locks, and KeysOf the
// keys a caller created and owns, for the User view;
// KeyOwner reads an admitted active owner with their latest_groups as
// teams. A key's first use in SeatWindow takes it a
// seat, checked against the seats limit through the same Entitlements in
// the transaction that records the use, counting each active or pending
// member once
// and each other live key that is not personal and was used in the window;
// Seats counts the same for the seats read, leaving out no key.
// space.Store locks an account's tree through its top space's row for
// each change, and writes the change's closure rows in its transaction:
// a move deletes the rows linking the subtree to its old ancestors and
// inserts the cross product of the new ancestors and the subtree.
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
// platform/tx, platform/audit for the account and organization trails,
// platform/crypt for the sealed column and platform/crypt/signer for the
// keys. Only the access domain's assembly
// imports it.
package pg
