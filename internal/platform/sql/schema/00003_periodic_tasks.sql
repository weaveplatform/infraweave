-- Periodic tasks: infraweave's own recurring work, such as queue.prune.
-- The scheduler ticker fires a due row as a job of the kind the row names
-- and moves next_run_at past now by whole intervals.
-- ref: decision 0011

CREATE TABLE periodic_tasks (
    name        text        PRIMARY KEY,
    interval    interval    NOT NULL CHECK (interval > interval '0'),
    next_run_at timestamptz NOT NULL
);
COMMENT ON TABLE periodic_tasks IS
    'global: installation work, which belongs to no account';

CREATE INDEX periodic_tasks_due ON periodic_tasks (next_run_at);
