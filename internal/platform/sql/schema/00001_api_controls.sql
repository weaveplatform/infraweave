-- Decision 0003's controls, shared by every serve replica (decision 0005,
-- "The stores of 0003"). Each is keyed by the caller: a client address
-- until requests carry an authenticated principal.

CREATE TABLE idempotency_keys (
    caller       text        NOT NULL,
    key          text        NOT NULL,
    fingerprint  text        NOT NULL,
    state        text        NOT NULL CHECK (state IN ('in_flight', 'done')),
    locked_until timestamptz NOT NULL,
    status       integer,
    content_type text,
    location     text,
    body         bytea,
    created_at   timestamptz NOT NULL,
    expires_at   timestamptz NOT NULL,
    PRIMARY KEY (caller, key),
    CHECK ((state = 'done') = (status IS NOT NULL))
);
COMMENT ON TABLE idempotency_keys IS
    'global: keyed by caller, which spans accounts until requests carry a principal';

-- UNLOGGED: a crash empties the buckets, which gives every caller a full
-- one for a moment, and in return no request writes WAL.
CREATE UNLOGGED TABLE rate_limit_buckets (
    caller      text             NOT NULL,
    class       text             NOT NULL CHECK (class IN ('standard', 'expensive')),
    tokens      double precision NOT NULL,
    refilled_at timestamptz      NOT NULL,
    PRIMARY KEY (caller, class)
);
COMMENT ON TABLE rate_limit_buckets IS
    'global: keyed by caller, which spans accounts until requests carry a principal';

-- A caller here is answered 503 api-disabled until until, or for good when
-- it is null. A writer sends NOTIFY infraweave_invalidate,
-- 'api_disabled_callers' after each change, so every replica rereads it.
CREATE TABLE api_disabled_callers (
    caller text PRIMARY KEY,
    until  timestamptz,
    reason text NOT NULL DEFAULT ''
);
COMMENT ON TABLE api_disabled_callers IS
    'global: the operator''s switch, keyed by caller across accounts';
