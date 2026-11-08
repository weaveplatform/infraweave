-- The organization role and organization keys: what the Organization
-- tag's callers hold. Neither reaches an account.
-- ref: decisions 0006 and 0007

-- A member holding organization-admin. created_by is the principal that
-- granted it, as <kind>:<actor ID>, since a break-glass admin or an
-- organization key grants as no member.
CREATE TABLE organization_admins (
    organization_id uuid        NOT NULL,
    member_id       uuid        NOT NULL,
    created_by      text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, member_id),
    FOREIGN KEY (organization_id, member_id)
        REFERENCES members (organization_id, id) ON DELETE RESTRICT
);
COMMENT ON TABLE organization_admins IS
    'global: the organization role reaches no account';

-- An organization's keys, held by its admins. Only the secret's SHA-256 is
-- stored. creator_member_id is null when no member created the key: a
-- break-glass admin or another organization key.
CREATE TABLE organization_keys (
    organization_id   uuid        NOT NULL REFERENCES organizations ON DELETE RESTRICT,
    id                uuid        PRIMARY KEY,
    name              text        NOT NULL,
    secret_hash       bytea       NOT NULL CHECK (octet_length(secret_hash) = 32),
    creator_member_id uuid,
    expires_at        timestamptz,
    disabled_at       timestamptz,
    last_used_at      timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (organization_id, creator_member_id)
        REFERENCES members (organization_id, id) ON DELETE RESTRICT
);
COMMENT ON TABLE organization_keys IS
    'global: an organization key reaches no account and takes no seat';
CREATE INDEX organization_keys_organization ON organization_keys (organization_id, id);
CREATE INDEX organization_keys_creator ON organization_keys (organization_id, creator_member_id)
    WHERE creator_member_id IS NOT NULL;
