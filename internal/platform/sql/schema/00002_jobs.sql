-- Decision 0011's job queue. A row is a delivery mechanism, not a record of
-- the work: the change that asks for a job inserts it in its own
-- transaction, and pruning deletes it once it is done.

CREATE TABLE jobs (
    id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    queue        text        NOT NULL,
    kind         text        NOT NULL,
    -- IDs, never secrets; anything larger than 64 KiB goes to the inbox
    -- store and the payload carries its key.
    payload      jsonb       NOT NULL CHECK (octet_length(payload::text) <= 65536),
    -- The account whose entitlement the claim checks; NULL for
    -- installation work such as pruning.
    account_id   uuid,
    unique_key   text,
    ordering_key text,
    priority     smallint    NOT NULL CHECK (priority BETWEEN 0 AND 9),
    run_at       timestamptz NOT NULL,
    state        text        NOT NULL
        CHECK (state IN ('pending', 'running', 'succeeded', 'dead', 'cancelled')),
    attempt      integer     NOT NULL DEFAULT 0 CHECK (attempt >= 0),
    max_attempts integer     NOT NULL CHECK (max_attempts > 0),
    -- Incremented by every claim; each transition of a running job names
    -- the generation it claimed, so a worker whose lease lapsed and was
    -- claimed again changes nothing.
    generation   bigint      NOT NULL DEFAULT 0,
    lease_until  timestamptz,
    lease_owner  text,
    rerun        boolean     NOT NULL DEFAULT false,
    last_error   text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz,
    CHECK ((state = 'running') = (lease_until IS NOT NULL))
) WITH (autovacuum_vacuum_scale_factor = 0.01);
COMMENT ON TABLE jobs IS
    'global: jobs span every organization; only operator principals list them';

CREATE INDEX jobs_claim ON jobs (queue, priority, run_at, id) WHERE state = 'pending';
CREATE UNIQUE INDEX jobs_unique ON jobs (kind, unique_key)
    WHERE state IN ('pending', 'running') AND unique_key IS NOT NULL;
CREATE INDEX jobs_ordering ON jobs (ordering_key, id)
    WHERE ordering_key IS NOT NULL AND state IN ('pending', 'running');
CREATE INDEX jobs_lease ON jobs (lease_until) WHERE state = 'running';
