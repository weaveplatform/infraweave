CREATE TABLE event_deliveries (
    account_id uuid NOT NULL,
    event_id uuid NOT NULL,
    sink text NOT NULL CHECK (sink <> ''),
    target_key text NOT NULL CHECK (target_key <> ''),
    rule_index integer NOT NULL CHECK (rule_index >= 0),
    state text NOT NULL DEFAULT 'enqueued' CHECK (state IN ('enqueued')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, event_id, sink, target_key, rule_index)
);

CREATE INDEX event_deliveries_created_at ON event_deliveries (created_at);

COMMENT ON COLUMN event_deliveries.account_id IS
    'Plain account identity; events.purge removes its records after account.deleted.';
COMMENT ON COLUMN event_deliveries.event_id IS
    'Plain outbox event identity within the account; delivery records intentionally outlive outbox retention.';
