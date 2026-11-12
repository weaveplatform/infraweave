-- Runs: each account's runs, the state transitions of each, the run that
-- holds each stack's run lock, each stack's tracked run, the outputs of
-- its last applied run, and the log chunks and other objects each run's
-- worker uploaded.
-- ref: decisions 0004, 0005 and 0008; report 08

-- stack_id has no foreign key because stacks belong to the stacks domain:
-- a run holds its stack while it is written, and deleting the stack
-- deletes its runs through the domain's StackGuard. state_version counts
-- the run's transitions, and a transition that read another fails, so two
-- never both move a run; commit is the commit the run works on, as the
-- API's Commit; triggered_by is the login or the trigger that created it;
-- priority is low, normal or high; runtime_config is the runtime
-- configuration its trigger gave, which overrides the stack's, and
-- force_apply_mode SINGLE or CASCADE when it applies with no changes.
-- priority_tier is kernel.RunTier of priority, type and drift_detection,
-- 1 first, the order a pool's workers claim waiting runs in.
-- worker_pool_id is the stack's private pool when the run was created,
-- NULL for the public pool, and worker_id the worker that last claimed it.
-- Neither has a foreign key, because pools and workers belong to the
-- workers domain, and neither is a held key: a run keeps the pool it
-- waited in and the worker that ran it after either is gone.
-- artifact_key_sealed is the run's 256-bit artifact key, sealed by the
-- keyring, made the first time the run's specification is handed over;
-- the runner seals the run's artifacts with it.
CREATE TABLE runs (
    account_id       uuid        NOT NULL,
    id               uuid        NOT NULL,
    stack_id         uuid        NOT NULL,
    type             text        NOT NULL,
    state            text        NOT NULL,
    state_version    bigint      NOT NULL DEFAULT 1,
    state_changed_at timestamptz NOT NULL DEFAULT now(),
    commit           jsonb       NOT NULL,
    triggered_by     text        NOT NULL,
    priority         text        NOT NULL DEFAULT 'normal',
    drift_detection  boolean     NOT NULL DEFAULT false,
    runtime_config   jsonb,
    force_apply_mode text,
    priority_tier    smallint    NOT NULL DEFAULT 3,
    worker_pool_id   uuid,
    worker_id        uuid,
    artifact_key_sealed bytea,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, id),
    CHECK (priority IN ('low', 'normal', 'high')),
    CHECK (priority_tier BETWEEN 1 AND 7),
    CHECK (force_apply_mode IN ('SINGLE', 'CASCADE'))
);

-- A stack's run lists read its runs of some types newest first, and its
-- runs in a time range by creation; a run search filters an account's runs
-- by state.
CREATE INDEX runs_stack ON runs (account_id, stack_id, type, id DESC);
CREATE INDEX runs_stack_created ON runs (account_id, stack_id, created_at);
CREATE INDEX runs_state ON runs (account_id, state);

-- A run token names its run by ID alone, with no account, so an ID names
-- one run in the installation and the worker API finds the run by it.
CREATE UNIQUE INDEX runs_id ON runs (id);

-- A worker's claim takes the first of its pool's waiting runs in this
-- order; the states are the ones a claim moves a run out of.
CREATE INDEX runs_claimable ON runs (worker_pool_id, priority_tier, created_at, id)
    WHERE state IN ('READY', 'CONFIRMED', 'REPLAN_REQUESTED');

-- A claim reads whether its worker holds a run already, and how many of
-- its pool's drift runs workers hold.
CREATE INDEX runs_worker ON runs (worker_id, state) WHERE worker_id IS NOT NULL;
CREATE INDEX runs_drift ON runs (worker_pool_id, state) WHERE drift_detection;

-- The reap reads the runs a claim moved to a preparing state, oldest
-- first, to fail those no started report moved on within their pool's
-- preparing timeout.
CREATE INDEX runs_preparing ON runs (state_changed_at, id)
    WHERE state IN ('PREPARING', 'PREPARING_APPLY', 'PREPARING_REPLAN');

-- Each transition a run went through, sequence being the state_version it
-- moved the run to. until is when a timed state ends; username is who
-- moved it, absent for the server; terminal is whether the state ends the
-- run, and has_logs whether the state wrote logs.
CREATE TABLE run_state_transitions (
    account_id uuid        NOT NULL,
    run_id     uuid        NOT NULL,
    sequence   bigint      NOT NULL,
    state      text        NOT NULL,
    timestamp  timestamptz NOT NULL DEFAULT now(),
    until      timestamptz,
    username   text,
    note       text,
    terminal   boolean     NOT NULL,
    has_logs   boolean     NOT NULL DEFAULT false,
    PRIMARY KEY (account_id, run_id, sequence),
    FOREIGN KEY (account_id, run_id) REFERENCES runs (account_id, id) ON DELETE CASCADE
);

-- The run that holds each stack's run lock, which one blocking run at a
-- time takes; a stack without a row is unlocked. stack_id has no foreign
-- key, as in runs.
CREATE TABLE stack_run_locks (
    account_id uuid        NOT NULL,
    stack_id   uuid        NOT NULL,
    run_id     uuid        NOT NULL,
    locked_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, stack_id),
    FOREIGN KEY (account_id, run_id) REFERENCES runs (account_id, id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX stack_run_locks_run ON stack_run_locks (account_id, run_id);

-- Each stack's tracked run, the run whose state is the stack's state; a
-- stack without a row has had none. stack_id has no foreign key, as in
-- runs.
CREATE TABLE stack_run_state (
    account_id     uuid NOT NULL,
    stack_id       uuid NOT NULL,
    tracked_run_id uuid NOT NULL,
    PRIMARY KEY (account_id, stack_id),
    FOREIGN KEY (account_id, tracked_run_id) REFERENCES runs (account_id, id) ON DELETE CASCADE
);

CREATE INDEX stack_run_state_run ON stack_run_state (account_id, tracked_run_id);

-- The outputs of each stack's last applied run, by name, with the run that
-- wrote them. stack_id has no foreign key, as in runs.
CREATE TABLE stack_outputs (
    account_id  uuid    NOT NULL,
    stack_id    uuid    NOT NULL,
    name        text    NOT NULL,
    run_id      uuid    NOT NULL,
    value       jsonb   NOT NULL,
    sensitive   boolean NOT NULL DEFAULT false,
    description text,
    PRIMARY KEY (account_id, stack_id, name),
    FOREIGN KEY (account_id, run_id) REFERENCES runs (account_id, id) ON DELETE CASCADE
);

CREATE INDEX stack_outputs_run ON stack_outputs (account_id, run_id);

-- The log chunks each run's worker uploaded and indexed, by the state
-- version they were written in and their sequence number there, which
-- readers tail. state is the state of that state version; object_key is
-- the chunk's key in the run-logs store,
-- <account>/<run>/<state>-<state_version>/<seq>; bytes is its size, and
-- first_at and last_at when its first and last lines were written. An
-- account's purge deletes each chunk's object with its row; a run deleted
-- with its stack leaves its objects to the store's retention.
CREATE TABLE run_log_chunks (
    account_id    uuid        NOT NULL,
    run_id        uuid        NOT NULL,
    state_version bigint      NOT NULL,
    seq           bigint      NOT NULL,
    state         text        NOT NULL,
    object_key    text        NOT NULL,
    bytes         bigint      NOT NULL,
    first_at      timestamptz NOT NULL,
    last_at       timestamptz NOT NULL,
    PRIMARY KEY (account_id, run_id, state_version, seq),
    FOREIGN KEY (account_id, run_id) REFERENCES runs (account_id, id) ON DELETE CASCADE,
    CHECK (seq >= 0 AND bytes >= 0 AND last_at >= first_at)
);

-- The other objects each run's worker uploaded and recorded, by the state
-- version they were uploaded in, their kind and their name: an artifact,
-- in run-artifacts, sealed with the run's artifact key; the workspace, in
-- workspaces, encrypted to the pool's key; or an observability artifact,
-- in run-observability. object_key is <account>/<run>/<state>-<state_version>/<name>
-- in that store, and size and sha256 the bytes as uploaded. Objects go as
-- a log chunk's do.
CREATE TABLE run_artifacts (
    account_id    uuid        NOT NULL,
    run_id        uuid        NOT NULL,
    state_version bigint      NOT NULL,
    kind          text        NOT NULL,
    name          text        NOT NULL,
    object_key    text        NOT NULL,
    size          bigint      NOT NULL,
    sha256        bytea       NOT NULL,
    recorded_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, run_id, state_version, kind, name),
    FOREIGN KEY (account_id, run_id) REFERENCES runs (account_id, id) ON DELETE CASCADE,
    CHECK (kind IN ('artifact', 'workspace', 'observability'))
);
