-- State: each stack's workspaces, the locks Terraform and OpenTofu hold on
-- them, and the managed state versions written to them.
-- ref: decisions 0004, 0005 and 0010; report 28 section 12.3

-- A stack's workspaces, name empty for its default workspace, which its
-- http backend reaches. stack_id has no foreign key because stacks belong
-- to the stacks domain: a workspace holds its stack while it is written,
-- and deleting the stack deletes its workspaces through the domain's
-- StackGuard. current_version_id is the version a read answers, absent
-- until the first write; it has no foreign key, since a version names its
-- workspace and both cannot name the other at insert.
CREATE TABLE state_workspaces (
    account_id         uuid        NOT NULL,
    stack_id           uuid        NOT NULL,
    name               text        NOT NULL,
    current_version_id uuid,
    created_at         timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, stack_id, name),
    CHECK (octet_length(name) <= 512)
);

-- The locks held on each workspace, by the ID of the LockInfo that took
-- them: shared ones while runs plan, or one exclusive one while a run
-- writes. info is the LockInfo as the client sent it, which a refused lock
-- answers; json keeps its bytes. run_id is the run that holds it, which
-- has no foreign key because runs belong to the runs domain: the run's
-- terminal run_updated deletes its locks.
CREATE TABLE state_locks (
    account_id uuid        NOT NULL,
    stack_id   uuid        NOT NULL,
    workspace  text        NOT NULL,
    lock_id    text        NOT NULL,
    mode       text        NOT NULL,
    run_id     uuid        NOT NULL,
    info       json        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, stack_id, workspace, lock_id),
    FOREIGN KEY (account_id, stack_id, workspace)
        REFERENCES state_workspaces (account_id, stack_id, name) ON DELETE CASCADE,
    CHECK (mode IN ('shared', 'exclusive')),
    CHECK (octet_length(info::text) <= 16384)
);

CREATE INDEX state_locks_run ON state_locks (account_id, run_id);

-- Each version written to a workspace, never overwritten. object_key is
-- its sealed object's key in the states store,
-- <account>/<stack>/<sha256 of workspace name>/<version>, and size and
-- sha256 its plaintext's. serial and lineage are the state's, absent for
-- a client-encrypted state the server never parses. run_id is the run
-- that wrote it, absent for an import or a rollback, with no foreign key,
-- as in state_locks; rolled_back_from is the version a rollback copied.
CREATE TABLE managed_state_versions (
    account_id        uuid        NOT NULL,
    id                uuid        NOT NULL,
    stack_id          uuid        NOT NULL,
    workspace         text        NOT NULL,
    object_key        text        NOT NULL,
    size              bigint      NOT NULL,
    sha256            bytea       NOT NULL,
    serial            bigint,
    lineage           text,
    client_encrypted  boolean     NOT NULL DEFAULT false,
    run_id            uuid,
    rollback          boolean     NOT NULL DEFAULT false,
    rollback_username text,
    rolled_back_from  uuid,
    imported          boolean     NOT NULL DEFAULT false,
    imported_username text,
    imported_at       timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, id),
    FOREIGN KEY (account_id, stack_id, workspace)
        REFERENCES state_workspaces (account_id, stack_id, name) ON DELETE CASCADE,
    CHECK (size >= 0 AND octet_length(sha256) = 32),
    CHECK (client_encrypted OR (serial IS NOT NULL AND lineage IS NOT NULL))
);

-- A stack's version search lists a workspace's versions by when they were
-- written; the index also serves the foreign key to the workspace.
CREATE INDEX managed_state_versions_search
    ON managed_state_versions (account_id, stack_id, workspace, created_at);

-- A run's versions, which the orphan count and a run's view read.
CREATE INDEX managed_state_versions_run ON managed_state_versions (account_id, run_id)
    WHERE run_id IS NOT NULL;
