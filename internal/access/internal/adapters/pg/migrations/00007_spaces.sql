-- Spaces: each account's tree under its top space, which the account's
-- slug addresses, and the closure table that holds every ancestor of
-- every space, so subtree joins and the evaluator never recurse. Creating
-- an account seeds its top space, with the account's ID, and legacy
-- beneath it.
-- ref: decision 0007

-- parent_id is null only for the top space, which nothing sits above.
-- The parent link is NO ACTION rather than RESTRICT, so deleting an
-- account's whole tree in one statement is checked once the statement
-- ends rather than row by row.
CREATE TABLE spaces (
    account_id       uuid        NOT NULL REFERENCES accounts ON DELETE RESTRICT,
    id               uuid        PRIMARY KEY,
    slug             text        NOT NULL,
    name             text        NOT NULL,
    description      text        NOT NULL DEFAULT '',
    labels           text[]      NOT NULL DEFAULT '{}',
    inherit_entities boolean     NOT NULL DEFAULT false,
    parent_id        uuid,
    created_at       timestamptz NOT NULL DEFAULT now(),
    -- Raised by every change of the row; the API serves it as the space's
    -- ETag.
    version          bigint      NOT NULL DEFAULT 1,
    UNIQUE (account_id, slug),
    UNIQUE (account_id, id),
    FOREIGN KEY (account_id, parent_id) REFERENCES spaces (account_id, id)
);

CREATE INDEX spaces_parent ON spaces (account_id, parent_id);
CREATE UNIQUE INDEX spaces_top ON spaces (account_id) WHERE parent_id IS NULL;

-- One row per ancestor of each space, the space itself included at depth
-- 0, written in the transaction that changes spaces.
CREATE TABLE space_closure (
    account_id    uuid    NOT NULL REFERENCES accounts ON DELETE RESTRICT,
    ancestor_id   uuid    NOT NULL REFERENCES spaces ON DELETE RESTRICT,
    descendant_id uuid    NOT NULL REFERENCES spaces ON DELETE RESTRICT,
    depth         integer NOT NULL CHECK (depth >= 0),
    PRIMARY KEY (ancestor_id, descendant_id)
);

CREATE INDEX space_closure_descendant ON space_closure (descendant_id);
CREATE INDEX space_closure_account ON space_closure (account_id);
