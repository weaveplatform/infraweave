-- An organization's identity providers, the OIDC, SAML and GitHub OAuth
-- sign-in front ends its members sign in through, and the identities that
-- sign in as each member.
-- ref: decision 0006

-- Any number per organization, one per kind its default, which the SSO
-- operations address. settings holds the kind's settings and no secret;
-- secret_sealed the OIDC client secret, the SAML service provider's
-- private key or the GitHub OAuth client secret, sealed to the row's id.
-- A provider is disabled until a sign-in through it succeeds: enabled_at
-- records when, and admin_login that sign-in's login.
-- ref: decision 0013
CREATE TABLE identity_providers (
    organization_id uuid        NOT NULL REFERENCES organizations ON DELETE RESTRICT,
    id              uuid        PRIMARY KEY,
    kind            text        NOT NULL CHECK (kind IN ('OIDC', 'SAML', 'GITHUB_OAUTH')),
    name            text        NOT NULL,
    settings        jsonb       NOT NULL,
    secret_sealed   bytea       NOT NULL,
    is_default      boolean     NOT NULL DEFAULT false,
    enabled_at      timestamptz,
    admin_login     text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, id),
    CHECK ((enabled_at IS NULL) = (admin_login IS NULL))
);
COMMENT ON TABLE identity_providers IS
    'global: an identity provider signs in to the organization, whichever account the member enters';
CREATE UNIQUE INDEX identity_providers_default ON identity_providers (organization_id, kind)
    WHERE is_default;

-- The identities that sign in as a member: one row per identity provider
-- subject. Two identities are two members until an organization admin
-- links them; nothing matches them by email, since an identity provider
-- may let anyone claim any address. login is the login the identity's
-- first sign-in asserted, which the API shows as its external username.
CREATE TABLE member_identities (
    organization_id      uuid        NOT NULL,
    member_id            uuid        NOT NULL,
    identity_provider_id uuid        NOT NULL,
    subject              text        NOT NULL,
    login                text        NOT NULL DEFAULT '',
    created_at           timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (identity_provider_id, subject),
    FOREIGN KEY (organization_id, member_id)
        REFERENCES members (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, identity_provider_id)
        REFERENCES identity_providers (organization_id, id) ON DELETE RESTRICT
);
COMMENT ON TABLE member_identities IS
    'global: an identity signs in to the organization, whichever account the member enters';
CREATE INDEX member_identities_member ON member_identities (organization_id, member_id);
CREATE INDEX member_identities_provider ON member_identities (organization_id, identity_provider_id);
