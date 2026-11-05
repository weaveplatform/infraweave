-- Decision 0013's keyring: data keys wrapped by a KEK that never enters
-- the database. Values sealed under a key carry its ID; a key is deleted
-- only once resealing leaves nothing under it.

CREATE TABLE encryption_keys (
    id          integer     PRIMARY KEY CHECK (id > 0),
    wrapped_key bytea       NOT NULL,
    -- The KEK that wrapped the key, so a rewrap knows what it replaces.
    kek_ref     text        NOT NULL,
    state       text        NOT NULL CHECK (state IN ('active', 'retiring', 'retired')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    retired_at  timestamptz,
    CHECK ((state = 'retired') = (retired_at IS NOT NULL))
);
COMMENT ON TABLE encryption_keys IS
    'global: the installation''s keyring, which seals every account''s values';

-- Exactly one key seals new values.
CREATE UNIQUE INDEX encryption_keys_one_active ON encryption_keys ((true)) WHERE state = 'active';
