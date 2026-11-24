-- The sign-ins started through an organization's OIDC providers, and the
-- back-channel logout tokens those providers sent.
-- ref: decision 0006 § "Human login"

-- A started sign-in, until its callback consumes it once within
-- expires_at, ten minutes on. The state and the browser's
-- __Host-infraweave_sso cookie are stored as their SHA-256, so a row read
-- from the database starts no callback; the PKCE verifier is sealed to the
-- row's id. A row goes with its provider, as it means nothing without it,
-- and authn.sessions.expire deletes it once it has ended.
-- ref: decision 0013
CREATE TABLE sso_requests (
    id                   uuid        PRIMARY KEY,
    organization_id      uuid        NOT NULL,
    identity_provider_id uuid        NOT NULL,
    purpose              text        NOT NULL
        CHECK (purpose IN ('sign-in', 'enable', 'link', 'reauthenticate')),
    link_id              uuid,
    state_hash           bytea       NOT NULL CHECK (octet_length(state_hash) = 32),
    nonce                text        NOT NULL,
    pkce_verifier_sealed bytea       NOT NULL,
    browser_hash         bytea       NOT NULL CHECK (octet_length(browser_hash) = 32),
    return_to            text        NOT NULL DEFAULT '/',
    created_at           timestamptz NOT NULL DEFAULT now(),
    expires_at           timestamptz NOT NULL,
    consumed_at          timestamptz,
    FOREIGN KEY (organization_id, identity_provider_id)
        REFERENCES identity_providers (organization_id, id) ON DELETE CASCADE,
    CHECK ((purpose = 'link') = (link_id IS NOT NULL))
);
COMMENT ON TABLE sso_requests IS
    'global: a sign-in authenticates at the organization, before any account admits the member';
CREATE UNIQUE INDEX sso_requests_state ON sso_requests (state_hash);
CREATE INDEX sso_requests_provider ON sso_requests (organization_id, identity_provider_id);
-- authn.sessions.expire's scan for ended requests.
CREATE INDEX sso_requests_expires ON sso_requests (expires_at);

-- The jti of each back-channel logout token a provider sent, until it
-- expires, so a replayed token ends nothing twice and is refused.
CREATE TABLE oidc_logout_tokens (
    identity_provider_id uuid        NOT NULL REFERENCES identity_providers ON DELETE CASCADE,
    jti                  text        NOT NULL,
    expires_at           timestamptz NOT NULL,
    PRIMARY KEY (identity_provider_id, jti)
);
COMMENT ON TABLE oidc_logout_tokens IS
    'global: a provider logs a person out of the organization, whichever account the session entered';
-- authn.sessions.expire's scan for expired tokens.
CREATE INDEX oidc_logout_tokens_expires ON oidc_logout_tokens (expires_at);
