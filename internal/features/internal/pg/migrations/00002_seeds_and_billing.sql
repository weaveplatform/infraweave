-- The content each seeded tier was last given by a release. A release
-- replaces a seeded tier's content only while it still equals this row, so
-- an operator's edit survives every later release, and a seeded tier the
-- operator deleted, whose row stays here, is never installed again.
CREATE TABLE tier_seeds (
    tier       text        PRIMARY KEY,
    features   text[]      NOT NULL,
    limits     jsonb       NOT NULL,
    seeded_at  timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE tier_seeds IS
    'global: the installation''s record of the seeded tiers it installed';

-- The billing switch: off by default, and while it is off no entitlement
-- check runs. One row.
CREATE TABLE billing (
    id           boolean     PRIMARY KEY DEFAULT true CHECK (id),
    enabled      boolean     NOT NULL DEFAULT false,
    updated_at   timestamptz NOT NULL DEFAULT now(),
    -- The tier a new organization gets. NULL is Enterprise, which the seed
    -- installs after the migrations run, so the row inserted here cannot
    -- name it.
    default_tier text        REFERENCES tiers ON DELETE RESTRICT
);
COMMENT ON TABLE billing IS
    'global: the switch belongs to the installation, not to a tier';
CREATE INDEX billing_default_tier ON billing (default_tier);
INSERT INTO billing DEFAULT VALUES;
