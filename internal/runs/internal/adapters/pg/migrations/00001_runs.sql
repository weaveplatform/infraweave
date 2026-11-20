-- Runs: each account's runs, the state transitions of each, the comments
-- on each, the run that holds each stack's run lock, each stack's tracked run, the outputs of
-- its last applied run, and the log chunks and other objects each run's
-- worker uploaded.
-- ref: decisions 0004, 0005 and 0008; report 08

-- stack_id has no foreign key because stacks belong to the stacks domain:
-- a run holds its stack while it is written, and deleting the stack
-- deletes its runs through the domain's StackGuard. state_version counts
-- the run's transitions, and a transition that read another fails, so two
-- never both move a run; commit is the commit the run works on, as the
-- API's Commit, with the branch the run was created for; triggered_by is
-- the login or the trigger that created it; priority is low, normal or
-- high; runtime_config is the trigger's typed runtime configuration, with
-- variable values redacted and supplied through env_overrides_sealed,
-- which overrides the stack's, and force_apply_mode SINGLE or CASCADE when
-- it applies with no changes. resolved_config is the immutable envelope of its public
-- runtime configuration and private execution settings, written at its first
-- successful specification transaction, with its token, key and sealed
-- answer. The earlier claim is separate. It holds only variable keys, no values,
-- mounted files, identity or credentials; NULL until that handover succeeds.
-- local_workspace is the key, in the uploads
-- store, of the local workspace a run proposed from one previews instead
-- of a commit of the repository, NULL for a run on a commit. delta
-- is its plan's delta, {add, change, delete, import, move, forget,
-- resources, outputs}, which the plan decision read, absent until the run
-- planned.
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
-- env_overrides_sealed holds a proposed run's environment variable overrides
-- or a trigger's or repository file's runtime variables: a JSON array of {name, value},
-- sealed by the keyring as they may hold secrets. A plain trigger alone may
-- reuse an equal active run; custom configuration always creates another.
-- command and skip_initialization are a task's: the command it performs,
-- and whether it performs it without initializing the tool first. A TASK
-- run alone has a command.
-- replan_targets are the resource addresses the last runTargetedReplan of
-- a tracked run named, which the specification of the run claimed from
-- REPLAN_REQUESTED carries; NULL until one did. kernel.MaxTargets bounds
-- their number.
-- stop_* and kill_* are a caller's request to stop or to kill the run:
-- when it was made, the login that made it and its note, which the
-- transition the signal ends the run with records, and when a poll
-- delivered it to the run's worker, NULL until one did. A poll delivers
-- each once, as a second SIGINT makes the tool exit mid-operation.
-- logs_deleted_at is when a caller deleted the run's logs ahead of their
-- retention, NULL until one did.
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
    resolved_config  jsonb,
    force_apply_mode text,
    local_workspace  text,
    delta            jsonb,
    priority_tier    smallint    NOT NULL DEFAULT 3,
    worker_pool_id   uuid,
    worker_id        uuid,
    artifact_key_sealed bytea,
    env_overrides_sealed bytea,
    command          text,
    skip_initialization boolean NOT NULL DEFAULT false,
    replan_targets   text[],
    stop_requested_at timestamptz,
    stop_requested_by text,
    stop_note        text,
    stop_delivered_at timestamptz,
    kill_requested_at timestamptz,
    kill_requested_by text,
    kill_note        text,
    kill_delivered_at timestamptz,
    logs_deleted_at  timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, id),
    CHECK (priority IN ('low', 'normal', 'high')),
    CHECK (priority_tier BETWEEN 1 AND 7),
    CHECK (force_apply_mode IN ('SINGLE', 'CASCADE')),
    CHECK ((type = 'TASK') = (command IS NOT NULL)),
    CHECK (replan_targets IS NULL
        OR type = 'TRACKED' AND cardinality(replan_targets) BETWEEN 1 AND 100),
    CHECK (kill_requested_at IS NULL OR stop_requested_at IS NOT NULL)
);

-- A stack's run lists read its runs of some types newest first, and its
-- runs in a time range by creation; a run search filters an account's runs
-- by state.
CREATE INDEX runs_stack ON runs (account_id, stack_id, type, id DESC);
-- Commit membership reads all types, states and triggers. Putting the hash
-- before type also serves event deduplication's full equality check under the
-- stack creation lock; an ended run suppresses another event of its type/commit.
CREATE INDEX runs_commit ON runs (account_id, stack_id, (commit->>'hash'), type);
CREATE INDEX runs_stack_created ON runs (account_id, stack_id, created_at, id);
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

-- run.timers reads the runs in the states it ends, oldest state change
-- first.
CREATE INDEX runs_waiting ON runs (state, state_changed_at, id);

-- Each transition a run went through, sequence being the state_version it
-- moved the run to. until is when a timed state ends; username is who
-- moved it, absent for the server; terminal is whether the state ends the
-- run, has_logs whether the state wrote logs, and log_chunks how many log
-- chunks the worker's report of the state's end says it cut, absent until
-- that report.
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
    log_chunks bigint      CHECK (log_chunks >= 0),
    PRIMARY KEY (account_id, run_id, sequence),
    FOREIGN KEY (account_id, run_id) REFERENCES runs (account_id, id) ON DELETE CASCADE
);

-- Historical policy IDs are account-qualified snapshots, not live cross-domain
-- foreign keys: deleting a policy keeps the evidence for an existing run.
CREATE TABLE run_policy_receipts (
    account_id uuid NOT NULL,
    run_id uuid NOT NULL,
    id uuid NOT NULL,
    state_version bigint NOT NULL,
    ordinal bigint NOT NULL,
    policy_id uuid NOT NULL,
    policy_name text NOT NULL,
    policy_version bigint NOT NULL CHECK (policy_version > 0),
    policy_type text NOT NULL,
    engine_type text NOT NULL,
    outcome text NOT NULL,
    decision jsonb NOT NULL,
    flags text[] NOT NULL DEFAULT '{}',
    reset_flags text[] NOT NULL DEFAULT '{}',
    sample_requested boolean NOT NULL DEFAULT false,
    sample_evaluation_id uuid,
    error_code text,
    error_message text,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id, run_id, id),
    UNIQUE (account_id, id),
    UNIQUE (account_id, run_id, ordinal),
    FOREIGN KEY (account_id, run_id, state_version)
        REFERENCES run_state_transitions (account_id, run_id, sequence) ON DELETE CASCADE,
    CHECK ((error_code IS NULL) = (error_message IS NULL))
);

-- A flag remains visible while any policy owns it. Reset deletes one policy's
-- rows before adding that same evaluation's new flags.
CREATE TABLE run_flags (
    account_id uuid NOT NULL,
    run_id uuid NOT NULL,
    policy_id uuid NOT NULL,
    flag text NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id, run_id, flag, policy_id),
    FOREIGN KEY (account_id, run_id) REFERENCES runs (account_id, id) ON DELETE CASCADE
);

-- Each comment on a run: id is a ULID, so the comments read oldest first
-- by it; username is the login that wrote it, and body its Markdown, at
-- most as many characters as control.MaxComment.
CREATE TABLE run_comments (
    account_id uuid        NOT NULL,
    run_id     uuid        NOT NULL,
    id         uuid        NOT NULL,
    username   text        NOT NULL,
    body       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, run_id, id),
    FOREIGN KEY (account_id, run_id) REFERENCES runs (account_id, id) ON DELETE CASCADE,
    CHECK (char_length(body) BETWEEN 1 AND 10000)
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
-- wrote them. stack_id has no foreign key, as in runs. value_sealed is the
-- output's value as JSON, sealed to its row whether or not it is
-- sensitive. A sensitive output's is NULL unless both its stack and the
-- launcher that applied it allow its value to be kept, so no row holds a
-- value they did not allow, sealed or not.
CREATE TABLE stack_outputs (
    account_id   uuid    NOT NULL,
    stack_id     uuid    NOT NULL,
    name         text    NOT NULL,
    run_id       uuid    NOT NULL,
    value_sealed bytea,
    sensitive    boolean NOT NULL DEFAULT false,
    description  text,
    PRIMARY KEY (account_id, stack_id, name),
    FOREIGN KEY (account_id, run_id) REFERENCES runs (account_id, id) ON DELETE CASCADE,
    CHECK (value_sealed IS NOT NULL OR sensitive)
);

CREATE INDEX stack_outputs_run ON stack_outputs (account_id, run_id);

-- The log chunks each run's worker uploaded and indexed, by the state
-- version they were written in and their sequence number there, which
-- readers tail. state is the state of that state version; object_key is
-- the chunk's key in the run-logs store,
-- <account>/<run>/<state>-<state_version>/<seq>; bytes is its size, and
-- first_at and last_at when its first and last lines were written. A lost
-- chunk is one the worker gave up uploading: it has no object, and
-- readers find a line in its place. An account's purge deletes each
-- chunk's object with its row; a run deleted with its stack leaves its
-- objects to the store's retention.
CREATE TABLE run_log_chunks (
    account_id    uuid        NOT NULL,
    run_id        uuid        NOT NULL,
    state_version bigint      NOT NULL,
    seq           bigint      NOT NULL,
    state         text        NOT NULL,
    object_key    text,
    bytes         bigint      NOT NULL,
    first_at      timestamptz NOT NULL,
    last_at       timestamptz NOT NULL,
    lost          boolean     NOT NULL DEFAULT false,
    PRIMARY KEY (account_id, run_id, state_version, seq),
    FOREIGN KEY (account_id, run_id) REFERENCES runs (account_id, id) ON DELETE CASCADE,
    CHECK (seq >= 0 AND bytes >= 0 AND last_at >= first_at),
    CHECK ((object_key IS NULL) = lost)
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
