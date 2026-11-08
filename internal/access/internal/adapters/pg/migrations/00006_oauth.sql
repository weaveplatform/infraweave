-- The authorization server for MCP clients: registered clients,
-- authorization requests with their codes, and the refresh tokens a grant
-- has spent. A grant is a session row, whose secret_hash is its current
-- refresh token's and whose oauth_resource is its tokens' aud.
-- ref: decision 0006

-- A public client, registered by anyone (RFC 7591); authn.sessions.expire
-- deletes one unused for 30 days.
CREATE TABLE oauth_clients (
    id            uuid        PRIMARY KEY,
    name          text        NOT NULL DEFAULT '',
    redirect_uris text[]      NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_used_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX oauth_clients_last_used ON oauth_clients (last_used_at);
COMMENT ON TABLE oauth_clients IS
    'global: a client registers with the installation, before any account grants it access';

-- An authorization request waiting for consent until expires_at, then
-- its decision: an approval records the member, the account and the
-- SHA-256 of the code it issued; redeeming the code records the grant.
-- A request outlives its end by a day, so a code replayed meanwhile still
-- names the grant to revoke.
CREATE TABLE oauth_requests (
    id              uuid        PRIMARY KEY,
    client_id       uuid        NOT NULL REFERENCES oauth_clients ON DELETE CASCADE,
    redirect_uri    text        NOT NULL,
    resource        text        NOT NULL,
    scopes          text[]      NOT NULL,
    state           text        NOT NULL DEFAULT '',
    code_challenge  text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    decided_at      timestamptz,
    organization_id uuid,
    account_id      uuid,
    member_id       uuid,
    code_hash       bytea       CHECK (octet_length(code_hash) = 32),
    code_expires_at timestamptz,
    redeemed_at     timestamptz,
    session_id      uuid        REFERENCES sessions ON DELETE SET NULL,
    CHECK (code_hash IS NULL OR decided_at IS NOT NULL AND account_id IS NOT NULL
        AND member_id IS NOT NULL AND code_expires_at IS NOT NULL)
);

CREATE INDEX oauth_requests_client ON oauth_requests (client_id);
CREATE INDEX oauth_requests_expires ON oauth_requests (expires_at);
CREATE INDEX oauth_requests_session ON oauth_requests (session_id) WHERE session_id IS NOT NULL;
CREATE UNIQUE INDEX oauth_requests_code ON oauth_requests (code_hash) WHERE code_hash IS NOT NULL;
COMMENT ON TABLE oauth_requests IS
    'global: a request waits for consent before the member chooses its account';

-- The refresh tokens a grant has spent: presenting one again revokes the
-- grant. They go with the grant's row.
CREATE TABLE oauth_spent_refresh_tokens (
    hash       bytea       PRIMARY KEY CHECK (octet_length(hash) = 32),
    session_id uuid        NOT NULL REFERENCES sessions ON DELETE CASCADE,
    spent_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX oauth_spent_refresh_tokens_session ON oauth_spent_refresh_tokens (session_id);
COMMENT ON TABLE oauth_spent_refresh_tokens IS
    'global: a spent token names its grant, whose session row carries the account';
