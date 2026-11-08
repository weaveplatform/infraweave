-- The record of every object in object storage. A row is pending from
-- before its upload until the transaction that records the business change
-- marks it live, and deleting from the transaction that drops it until
-- blob.delete has removed the object. blob.expire deletes pending rows
-- older than 24 hours and live rows past expires_at with their objects, so
-- no object outlives its row and no bucket is listed.
-- ref: decision 0005

CREATE TABLE blob_objects (
    store              text        NOT NULL,
    -- The key within the store, without the store's prefix.
    key                text        NOT NULL,
    -- The plaintext's size and SHA-256. A signed upload declares both
    -- before it starts; a server-side write records the SHA-256 when its
    -- upload ends, so only a pending row lacks it.
    size               bigint      NOT NULL CHECK (size >= 0),
    sha256             bytea       CHECK (octet_length(sha256) = 32),
    state              text        NOT NULL CHECK (state IN ('pending', 'live', 'deleting')),
    -- The object's own content key, for an object in a sealed store.
    content_key_sealed bytea,
    expires_at         timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (store, key),
    CHECK (state = 'pending' OR sha256 IS NOT NULL)
);
COMMENT ON TABLE blob_objects IS
    'global: objects of every store; a key starts with its account, and each row that references an object carries it';

-- blob.expire's two scans.
CREATE INDEX blob_objects_expired ON blob_objects (expires_at) WHERE state = 'live';
CREATE INDEX blob_objects_abandoned ON blob_objects (created_at) WHERE state = 'pending';
