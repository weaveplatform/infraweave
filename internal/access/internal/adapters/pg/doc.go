// Package pg is the access domain's PostgreSQL adapter: its migration stream
// and the stores that implement the core's ports. The stream holds the
// tenancy (organizations, accounts with their api_key_non_admin_managers and
// allow_non_admin_space_creation settings, members, pending until an invited
// member first signs in, and the members each account admits, with each admission's latest_groups and slack_member_id), the
// credentials (API keys, whose personal owner is a member rather than an
// admission, so removing a managed user keeps the keys it disables,
// sessions, signing keys and each organization's break-glass credential),
// account_settings, a row created on the first write, so an account without
// one reads each column's default, organization_admins and
// organization_keys, account_invitations and managed_user_groups, the OAuth
// clients, requests and spent refresh tokens, spaces and space_closure,
// roles, role_actions and role_bindings, and the organization's
// identity_providers, one per kind its default by a partial unique index,
// with the member_identities that sign in through each. Creating an account, with its
// organization or by an organization admin, seeds its top space, legacy
// and system roles in the same transaction, and claiming or creating one
// binds space-admin on its top space to the account admin it names. A
// custom role's change locks its row, so a binding written beside a
// delete, whose foreign key reads the row with a share lock, waits for it
// and the count of bindings the delete reads stays true. A
// key's Admin reads whether it holds that binding. A binding of a
// role and space its actor holds already is a *binding.Duplicate naming
// its index; removing a managed user, or deleting a group or an API key,
// deletes its bindings in the same transaction; ReplaceAccessRules swaps
// an actor's access_rule bindings in one; and Atomic runs a function in
// one transaction that every write through its context joins. For the
// grant views, Candidates reads an account's managed users with their
// latest groups, its API keys but personal and deleted ones with their
// teams, or its live sessions of users or keys, and SearchHolders and
// SuggestHolders search a view's rows among the holders the evaluator
// found, each row's ID standing in for the space a search's scope holds
// rows to. Every write to the
// space tree or a space's inheritance, to roles, role_actions,
// role_bindings or managed_user_groups, and every admission, moves the
// account's authz_version and sends authz:<account> on
// infraweave_invalidate in its transaction, and deleting an account
// sends the message alone; the function WithSnapshots gives forgets this
// replica's snapshot of the account when the transaction commits. AuthzData
// reads an account's authorization rows in one serializable transaction
// and AuthzVersions the versions of the accounts a replica holds, as
// authz.Store.
// Creating an organization creates its entitlement through
// featuresapi.Entitlements in the same transaction. Store implements port.Tenancy, port.Sessions,
// port.FederationSubjects, reading an account's slug, subject template
// and a space's ancestry in one statement, port.APIKeys, port.OrganizationKeys, apikey.Store, operator.Store,
// orgadmin.Store, sso.Store, settings.Store, space.Store, user.Store, group.Store,
// role.Store, binding.Store, holder.Store and authz.Store, with an
// OIDC key's claim mapping as jsonb. operator.Store's writes record their
// organization trail entry through platform/audit in the same
// transaction; deleting an organization revokes its live sessions, then
// deletes its sessions, API keys, organization keys and admins,
// invitations, managed user groups, members
// and their identities, its
// break-glass credential, its accounts' settings, role bindings, roles and
// space trees, its accounts and itself, as every foreign key restricts.
// Deleting an account takes its
// organization's row lock, so two deletions never both remove an
// organization's last two accounts, then deletes the account's sessions,
// API keys, invitations, managed user groups, admissions, settings,
// role bindings, roles, space tree and the account, and records its
// organization trail entry in the same transaction. Each deletion appends
// an accessapi.Deleted per account it removes to the outbox WithEvents
// gives, in its transaction, so the other domains' purges remove their
// rows of it; Keys declares the accounts, organizations and spaces those
// domains hold, for the nightly orphan count; turning
// personal keys off with disableExistingKeys disables them for ACCOUNT
// and revokes their sessions. orgadmin.Store's writes record their
// organization trail entry in the same transaction: claiming an account
// locks it, and admitting an account admin checks the seat it takes,
// as a key's first use does; deactivating a member disables their
// personal keys as DEPROVISIONED and revokes their and those keys'
// sessions; disabling or deleting an organization key revokes its
// sessions. sso.Store's writes take the organization's row lock FOR NO KEY
// UPDATE before they read its providers, so two writes never both make a
// default of one kind, and record their trail entry in the same
// transaction: a provider's secret is sealed into
// identity_providers.secret_sealed, bound to its id, through the Sealer
// WithSealer gives, and ProviderSecret opens it; deleting a provider
// deletes its identities, revokes the sessions signed in through it and
// makes the oldest remaining provider of its kind the default when it was.
// sso.SignInStore keeps a started sign-in in sso_requests, its PKCE
// verifier sealed into pkce_verifier_sealed bound to its id; consuming it
// is one UPDATE of a live, unconsumed row matching the state's and the
// browser's hashes, so two callbacks consume it once. A sign-in takes the
// organization's row lock, as an invite does, resolves the identity to a
// member, checks a new member's seat, records the identity,
// last_seen_at and last_sign_in_kind, creates the session and seals its ID token into
// sessions.idp_id_token_sealed, and, for an enable, sets the provider's
// enabled_at and admin_login with its trail entry, all in one transaction;
// an account switch carries the ID token to the session it creates.
// IsOrganizationAdmin also admits a session signed in through one of the
// organization's providers as that provider's admin login. A back-channel
// logout's jti is kept in oidc_logout_tokens until it expires, its primary
// key accepting each once. ExpireSignIns deletes the ended requests and
// the expired logout tokens. user.Store's writes record their account trail entry in the
// same transaction: inviting a login takes its organization's row lock,
// admits its member or a new pending one and checks the seat a member no
// other account admits takes; removing a managed user deletes its
// invitations, disables its personal keys in the account as
// DEPROVISIONED and revokes their and its sessions there. A group's
// members are the admissions whose latest_groups hold its name, which a
// member session's account entry writes, in the entry's transaction, from
// the groups its sign-in reported, raising the admission's version when
// they change; another account's admission keeps its own. Creating
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
// inserts the cross product of the new ancestors and the subtree. Spaces,
// API keys, admissions and managed user groups each carry a version,
// raised by every UPDATE of the row but an API key's use, which moves
// last_used_at alone; a write given a kernel.Expect locks the row FOR
// UPDATE and checks its version first, and one the expect refuses is
// kernel.ErrStale, the transaction rolled back.
// A unique violation on a slug is
// port.ErrSlugTaken and a
// break-glass credential of no organization port.ErrNotFound. Store also
// implements port.BreakGlass, whose sign-in writes the session and its
// trail entry in one transaction, and session.Admission, from
// account_users. Every revocation sends session:<id> on
// infraweave_invalidate in its own transaction, and a member's new
// session in a channel revokes their live one there. SigningKeys
// implements token.Store for the api-token and federation purposes: under
// the local backend it generates each key for its step's algorithm and
// seals its private half into signing_keys.private_key_sealed bound to
// the key's id; under the kms backend it stores a KMS key's reference in
// kms_key_ref and signs through the opened KMS key of that reference
// (ErrKMSRef when none is, ErrKMSAlgorithm when it is opened for another
// algorithm). Each row's algorithm is its key's, under which a sealed
// half opens. It applies a rotation under a SHARE ROW
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
// It imports accessapi, the access domain's core packages and ports,
// featuresapi, internal/kernel, platform/outbox, platform/sql/integrity,
// pgx, and platform/sql, platform/sql/migrate, platform/sql/notify,
// platform/tx, platform/audit for the account and organization trails,
// platform/crypt for the sealed columns and platform/crypt/signer for the
// keys. Only the access domain's assembly
// imports it.
package pg
