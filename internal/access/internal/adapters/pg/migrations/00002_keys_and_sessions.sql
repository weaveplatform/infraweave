-- Credentials: an account's API keys, the session behind every token, and
-- the installation's signing keys.
-- ref: decision 0006

-- An API key belongs to one account, and its token works there only. Only
-- the secret's SHA-256 is stored. There is no admin column: a key is an
-- account admin through a space-admin binding on the account's top space.
CREATE TABLE api_keys (
    account_id              uuid        NOT NULL REFERENCES accounts ON DELETE RESTRICT,
    id                      uuid        NOT NULL,
    name                    text        NOT NULL,
    description             text        NOT NULL DEFAULT '',
    type                    text        NOT NULL CHECK (type IN ('SECRET', 'OIDC')),
    secret_hash             bytea       CHECK (octet_length(secret_hash) = 32),
    oidc_issuer             text,
    oidc_client_id          text,
    oidc_subject_expression text,
    oidc_claim_mapping      jsonb,
    teams                   text[]      NOT NULL DEFAULT '{}',
    is_personal             boolean     NOT NULL DEFAULT false,
    -- The member a personal key acts as; it never exceeds them. The key
    -- names the member rather than their admission because it outlives
    -- the admission: removing a managed user keeps their keys, disabled as
    -- DEPROVISIONED.
    owner_user_id           uuid,
    is_machine              boolean     NOT NULL DEFAULT false,
    creator                 text        NOT NULL,
    expires_at              timestamptz,
    disabled_at             timestamptz,
    disabled_reason         text        CHECK (disabled_reason IN ('ADMIN', 'ACCOUNT', 'DEPROVISIONED', 'SCHEME')),
    last_used_at            timestamptz,
    created_at              timestamptz NOT NULL DEFAULT now(),
    deleted_at              timestamptz,
    -- Raised by every change of the key but a use, which moves only
    -- last_used_at; the API serves it as the key's ETag.
    version                 bigint      NOT NULL DEFAULT 1,
    PRIMARY KEY (account_id, id),
    FOREIGN KEY (owner_user_id) REFERENCES members ON DELETE RESTRICT,
    -- A secret key has its hash; an OIDC key has its issuer, client and
    -- subject expression instead.
    CHECK (
        (type = 'SECRET' AND secret_hash IS NOT NULL AND oidc_issuer IS NULL) OR
        (type = 'OIDC' AND secret_hash IS NULL AND oidc_issuer IS NOT NULL
            AND oidc_client_id IS NOT NULL AND oidc_subject_expression IS NOT NULL)
    ),
    CHECK ((owner_user_id IS NOT NULL) = is_personal),
    CHECK ((disabled_reason IS NULL) OR (disabled_at IS NOT NULL))
);
-- The exchange finds a key by ID alone, before it knows the account.
CREATE UNIQUE INDEX api_keys_id ON api_keys (id);
CREATE INDEX api_keys_owner ON api_keys (account_id, owner_user_id) WHERE owner_user_id IS NOT NULL;
CREATE INDEX api_keys_owner_member ON api_keys (owner_user_id) WHERE owner_user_id IS NOT NULL;

-- Every token but the federation, worker and run tokens has a session row,
-- whose ID is the token's jti, so revoking the session revokes the token.
-- account_id is null for an organization, break-glass or operator
-- principal and for a member no account admits; member_id is null for all
-- but a person's sessions. Only an operator's session belongs to no
-- organization.
CREATE TABLE sessions (
    organization_id      uuid        REFERENCES organizations ON DELETE RESTRICT,
    account_id           uuid,
    id                   uuid        PRIMARY KEY,
    channel              text        NOT NULL
        CHECK (channel IN ('UNKNOWN', 'WEB', 'TERRAFORM_CLI', 'INFRAWEAVECTL')),
    actor_kind           text        NOT NULL
        CHECK (actor_kind IN ('user', 'api_key', 'run', 'organization', 'break_glass', 'operator')),
    actor_id             uuid        NOT NULL,
    member_id            uuid,
    login                text        NOT NULL DEFAULT '',
    name                 text        NOT NULL DEFAULT '',
    email                text        NOT NULL DEFAULT '',
    admin                boolean     NOT NULL DEFAULT false,
    teams                text[]      NOT NULL DEFAULT '{}',
    identity_provider_id uuid,
    idp_subject          text,
    oauth_client_id      text,
    scopes               text[]      NOT NULL DEFAULT '{}',
    ip_address           inet,
    user_agent           text        NOT NULL DEFAULT '',
    -- The SHA-256 of an opaque secret: a browser session's cookie or an
    -- OAuth grant's refresh token.
    secret_hash          bytea       CHECK (octet_length(secret_hash) = 32),
    -- A CLI session's browser session, which it outlives.
    parent_session_id    uuid        REFERENCES sessions ON DELETE RESTRICT,
    run_id               uuid,
    created_at           timestamptz NOT NULL DEFAULT now(),
    last_seen_at         timestamptz NOT NULL DEFAULT now(),
    idle_expires_at      timestamptz,
    expires_at           timestamptz NOT NULL,
    revoked_at           timestamptz,
    -- The MCP resource an OAuth grant's access tokens name as their aud.
    oauth_resource       text,
    FOREIGN KEY (organization_id, account_id)
        REFERENCES accounts (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, member_id)
        REFERENCES members (organization_id, id) ON DELETE RESTRICT,
    CHECK ((organization_id IS NULL) = (actor_kind = 'operator')),
    CHECK (account_id IS NULL OR actor_kind NOT IN ('organization', 'break_glass', 'operator'))
);
COMMENT ON TABLE sessions IS
    'global: a session may belong to an organization and no account';
CREATE INDEX sessions_account ON sessions (organization_id, account_id) WHERE account_id IS NOT NULL;
CREATE INDEX sessions_member ON sessions (organization_id, member_id) WHERE member_id IS NOT NULL;
CREATE INDEX sessions_actor ON sessions (actor_kind, actor_id);
CREATE INDEX sessions_parent ON sessions (parent_session_id) WHERE parent_session_id IS NOT NULL;
CREATE UNIQUE INDEX sessions_secret ON sessions (secret_hash) WHERE secret_hash IS NOT NULL;
-- authn.sessions.expire's scan for sessions 30 days past their end.
CREATE INDEX sessions_expires ON sessions (expires_at);

-- The installation's signing keys. id is the kid. A key's
-- private half is sealed under the keyring, or lives in a KMS that
-- kms_key_ref names; never both.
CREATE TABLE signing_keys (
    id                 uuid        PRIMARY KEY,
    purpose            text        NOT NULL CHECK (purpose IN ('api-token', 'federation')),
    algorithm          text        NOT NULL CHECK (algorithm IN ('ES256', 'RS256', 'PS256')),
    public_jwk         jsonb       NOT NULL,
    private_key_sealed bytea,
    kms_key_ref        text,
    state              text        NOT NULL CHECK (state IN ('next', 'active', 'retiring', 'retired')),
    created_at         timestamptz NOT NULL DEFAULT now(),
    activated_at       timestamptz,
    retire_after       timestamptz,
    retired_at         timestamptz,
    CHECK ((private_key_sealed IS NULL) <> (kms_key_ref IS NULL)),
    CHECK ((state = 'retired') = (retired_at IS NOT NULL))
);
COMMENT ON TABLE signing_keys IS
    'global: the installation''s keys sign every account''s tokens';
-- Exactly one key of each purpose signs.
CREATE UNIQUE INDEX signing_keys_one_active ON signing_keys (purpose) WHERE state = 'active';
