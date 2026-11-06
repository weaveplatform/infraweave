-- Decision 0007's tenancy below root: organizations, the accounts each
-- holds, the members who sign in to it, and the members each account
-- admits. Root has no row; only operator principals reach it. Slugs never
-- change, and names may.

CREATE TABLE organizations (
    id         uuid        PRIMARY KEY,
    slug       text        NOT NULL UNIQUE,
    name       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE organizations IS
    'global: organizations sit above every account';

-- Account slugs are unique across the installation, not per organization,
-- because the registry namespace and the federation audience name an
-- account without its organization.
CREATE TABLE accounts (
    organization_id uuid        NOT NULL REFERENCES organizations ON DELETE RESTRICT,
    id              uuid        PRIMARY KEY,
    slug            text        NOT NULL UNIQUE,
    name            text        NOT NULL,
    -- Bumped by every change to the account's authorization, so replicas
    -- know their cached snapshot is stale (decision 0007).
    authz_version   bigint      NOT NULL DEFAULT 0,
    -- Set once an organization admin names the first account admin; a
    -- claimed account never returns to unclaimed.
    claimed_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    -- The target of the composite keys that keep a member, a session or a
    -- key inside its account's organization.
    UNIQUE (organization_id, id)
);
COMMENT ON TABLE accounts IS
    'global: the accounts themselves, each its own data boundary';

CREATE TABLE members (
    organization_id uuid        NOT NULL REFERENCES organizations ON DELETE RESTRICT,
    id              uuid        PRIMARY KEY,
    login           text        NOT NULL,
    name            text        NOT NULL,
    email           text        NOT NULL,
    status          text        NOT NULL CHECK (status IN ('active', 'deactivated')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz,
    UNIQUE (organization_id, id)
);
COMMENT ON TABLE members IS
    'global: a member belongs to its organization and to every account that admits it';

-- The identities that sign in as a member: one row per identity provider
-- subject. Two identities are two members until an organization admin
-- links them; nothing matches them by email (decision 0006).
CREATE TABLE member_identities (
    organization_id      uuid        NOT NULL,
    member_id            uuid        NOT NULL,
    identity_provider_id uuid        NOT NULL,
    subject              text        NOT NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (identity_provider_id, subject),
    FOREIGN KEY (organization_id, member_id)
        REFERENCES members (organization_id, id) ON DELETE RESTRICT
);
COMMENT ON TABLE member_identities IS
    'global: an identity signs in to the organization, whichever account the member enters';
CREATE INDEX member_identities_member ON member_identities (organization_id, member_id);

-- The members an account admits: report 28's account users. A member
-- enters an account only through a row here, and only a member of the
-- account's own organization can have one.
CREATE TABLE account_users (
    account_id      uuid        NOT NULL,
    organization_id uuid        NOT NULL,
    member_id       uuid        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, member_id),
    FOREIGN KEY (organization_id, account_id)
        REFERENCES accounts (organization_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (organization_id, member_id)
        REFERENCES members (organization_id, id) ON DELETE RESTRICT
);
CREATE INDEX account_users_account ON account_users (organization_id, account_id);
CREATE INDEX account_users_member ON account_users (organization_id, member_id);

-- Each organization's break-glass credential, which only the operator sets
-- (decision 0006): a login and a PBKDF2-HMAC-SHA256 hash, never a password.
CREATE TABLE organization_break_glass (
    organization_id uuid        PRIMARY KEY REFERENCES organizations ON DELETE RESTRICT,
    login           text        NOT NULL,
    password_hash   text        NOT NULL,
    updated_at      timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE organization_break_glass IS
    'global: the credential signs in to the organization and reaches no account';

-- A slug is part of every external name an organization or account has, so
-- it never changes; a rename changes only the name.
CREATE FUNCTION access_slug_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.slug IS DISTINCT FROM OLD.slug THEN
        RAISE EXCEPTION 'the slug of % % cannot change', TG_TABLE_NAME, OLD.id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END
$$;
CREATE TRIGGER organizations_slug_immutable BEFORE UPDATE OF slug ON organizations
    FOR EACH ROW EXECUTE FUNCTION access_slug_immutable();
CREATE TRIGGER accounts_slug_immutable BEFORE UPDATE OF slug ON accounts
    FOR EACH ROW EXECUTE FUNCTION access_slug_immutable();
