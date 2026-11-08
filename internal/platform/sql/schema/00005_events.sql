-- The outbox: an event is written in the transaction of the change that
-- raised it, with its events.fanout job, and fanned out to its consumers
-- after the commit. A row not fanned out is never deleted.
-- ref: decision 0015

CREATE TABLE events (
    account_id     uuid        NOT NULL,
    id             uuid        NOT NULL,
    kind           text        NOT NULL,
    schema_version integer     NOT NULL CHECK (schema_version > 0),
    space          text,
    subject_kind   text        NOT NULL,
    subject_id     text        NOT NULL,
    -- The run's stateVersion, or a per-subject counter: what a receiver
    -- orders one subject's events by.
    sequence       bigint      NOT NULL,
    -- Nanoseconds since the Unix epoch, as the notification policy's
    -- input carries timestamps.
    occurred_at    bigint      NOT NULL,
    document       jsonb       NOT NULL,
    fanned_out_at  timestamptz,
    PRIMARY KEY (account_id, id)
);

-- The age of the oldest event not yet fanned out is a health signal;
-- pruning reads fanned-out rows by when they were.
-- ref: decision 0017
CREATE INDEX events_unfanned ON events (occurred_at) WHERE fanned_out_at IS NULL;
CREATE INDEX events_fanned_out ON events (fanned_out_at) WHERE fanned_out_at IS NOT NULL;

-- One row per consumer an event was fanned out to, so a fan-out that runs
-- twice enqueues no consumer twice.
CREATE TABLE event_consumptions (
    account_id uuid NOT NULL,
    event_id   uuid NOT NULL,
    consumer   text NOT NULL,
    PRIMARY KEY (account_id, event_id, consumer),
    FOREIGN KEY (account_id, event_id) REFERENCES events (account_id, id)
);
