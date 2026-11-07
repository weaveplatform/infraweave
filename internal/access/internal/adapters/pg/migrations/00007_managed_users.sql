-- Managed users: the pending member an invitation creates, the
-- invitations, what an account knows of a member it admits, and the
-- account's managed user groups.

-- A pending member is one an invitation created who has not signed in
-- yet. It holds a seat, as the invitation took one.
ALTER TABLE members DROP CONSTRAINT members_status_check;
ALTER TABLE members ADD CONSTRAINT members_status_check
    CHECK (status IN ('active', 'pending', 'deactivated'));

-- The groups the identity provider reported at the member's latest
-- sign-in to the account, which its managed user groups match by name,
-- and the Slack user that acts as the member there.
ALTER TABLE account_users
    ADD COLUMN latest_groups   text[] NOT NULL DEFAULT '{}',
    ADD COLUMN slack_member_id text;

-- A personal key outlives its owner's admission: removing a managed user
-- keeps their keys, disabled as DEPROVISIONED, so a key names its member
-- rather than the admission.
ALTER TABLE api_keys DROP CONSTRAINT api_keys_account_id_owner_user_id_fkey;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_owner_user_id_fkey
    FOREIGN KEY (owner_user_id) REFERENCES members ON DELETE RESTRICT;
CREATE INDEX api_keys_owner_member ON api_keys (owner_user_id) WHERE owner_user_id IS NOT NULL;

-- An invitation of a member to an account, recorded without e-mail
-- delivery. The person accepts it by signing in through its link, which
-- names its id, never by an e-mail match (decision 0006); it may be
-- accepted until 24 hours after sent_at, which a resend moves.
CREATE TABLE account_invitations (
    account_id uuid        NOT NULL,
    id         uuid        PRIMARY KEY,
    member_id  uuid        NOT NULL,
    email      text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL,
    sent_at    timestamptz NOT NULL,
    FOREIGN KEY (account_id, member_id)
        REFERENCES account_users (account_id, member_id) ON DELETE RESTRICT
);
CREATE INDEX account_invitations_user ON account_invitations (account_id, member_id);

-- An account's managed user groups. A group names an identity provider
-- group, and its members are the account's users whose latest_groups hold
-- the name, so a group reaches no other account.
CREATE TABLE managed_user_groups (
    account_id  uuid        NOT NULL REFERENCES accounts ON DELETE RESTRICT,
    id          uuid        PRIMARY KEY,
    name        text        NOT NULL,
    description text        NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL,
    UNIQUE (account_id, name)
);
