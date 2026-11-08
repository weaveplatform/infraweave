-- Tiers and entitlements. The features domain sits below access, which
-- owns organizations and accounts, so their IDs appear here without a
-- foreign key; access creates and deletes these rows through featuresapi
-- in the transaction that creates or deletes the organization or account.
-- ref: decisions 0004 and 0021

-- A tier is data: a name, a set of features and a set of limit values.
CREATE TABLE tiers (
    name       text        PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE tiers IS
    'global: tiers belong to the installation, which the operator edits';

CREATE TABLE tier_features (
    tier    text NOT NULL REFERENCES tiers ON DELETE RESTRICT,
    feature text NOT NULL,
    PRIMARY KEY (tier, feature)
);
COMMENT ON TABLE tier_features IS
    'global: a tier''s features';

-- A limit the tier has no row for has no limit.
CREATE TABLE tier_limits (
    tier       text   NOT NULL REFERENCES tiers ON DELETE RESTRICT,
    limit_name text   NOT NULL,
    value      bigint NOT NULL CHECK (value >= 0),
    PRIMARY KEY (tier, limit_name)
);
COMMENT ON TABLE tier_limits IS
    'global: a tier''s limit values';

-- One row per organization. Its lock serializes the limit checks of every
-- account of the organization, and entitlement_version, bumped by every
-- change to the tier or grants, tells replicas their snapshot is stale.
CREATE TABLE organization_entitlements (
    organization_id     uuid        PRIMARY KEY,
    -- Deleting a tier an organization holds is refused.
    tier                text        NOT NULL REFERENCES tiers ON DELETE RESTRICT,
    entitlement_version bigint      NOT NULL DEFAULT 0,
    created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX organization_entitlements_tier ON organization_entitlements (tier);
COMMENT ON TABLE organization_entitlements IS
    'global: an organization''s tier, which every account of it inherits';

-- A feature the operator grants an organization beyond its tier.
CREATE TABLE organization_feature_grants (
    organization_id uuid        NOT NULL REFERENCES organization_entitlements ON DELETE RESTRICT,
    feature         text        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, feature)
);
COMMENT ON TABLE organization_feature_grants IS
    'global: grants belong to the organization and survive a change of tier';

-- A limit value the operator sets for an organization above or below its
-- tier's; NULL lifts the limit.
CREATE TABLE organization_limit_grants (
    organization_id uuid        NOT NULL REFERENCES organization_entitlements ON DELETE RESTRICT,
    limit_name      text        NOT NULL,
    value           bigint      CHECK (value >= 0),
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, limit_name)
);
COMMENT ON TABLE organization_limit_grants IS
    'global: grants belong to the organization and survive a change of tier';

-- An account's ceiling below its organization's pool. The sum of an
-- organization's account limits may pass the pool: a ceiling, not a
-- reservation.
CREATE TABLE account_limits (
    organization_id uuid        NOT NULL REFERENCES organization_entitlements ON DELETE RESTRICT,
    account_id      uuid        NOT NULL,
    limit_name      text        NOT NULL,
    value           bigint      NOT NULL CHECK (value >= 0),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, limit_name)
);
CREATE INDEX account_limits_organization ON account_limits (organization_id);
