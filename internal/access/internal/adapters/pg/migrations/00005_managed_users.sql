-- Managed users: the invitations that admit a member to an account, and
-- the account's managed user groups.

-- An invitation of a member to an account, recorded without e-mail
-- delivery. The person accepts it by signing in through its link, which
-- names its id, never by an e-mail match; it may be accepted until 24
-- hours after sent_at, which a resend moves.
-- ref: decision 0006
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
    -- Raised by every change of the row; the API serves it as the group's
    -- ETag.
    version     bigint      NOT NULL DEFAULT 1,
    UNIQUE (account_id, name)
);
