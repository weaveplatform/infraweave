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

-- Decision 0021's billing switch: off by default, and while it is off no
-- entitlement check runs. One row.
CREATE TABLE billing (
    id         boolean     PRIMARY KEY DEFAULT true CHECK (id),
    enabled    boolean     NOT NULL DEFAULT false,
    updated_at timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE billing IS
    'global: the switch belongs to the installation, not to a tier';
INSERT INTO billing DEFAULT VALUES;
