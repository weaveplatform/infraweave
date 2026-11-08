-- Decision 0007's spaces: each account's tree under its top space, which
-- the account's slug addresses, and the closure table that holds every
-- ancestor of every space, so subtree joins and the evaluator never
-- recurse.

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

-- Every account that exists before this migration gets its top space and
-- legacy, as account creation seeds them. A top space's ID is its
-- account's, as seeding gives it, so the account names its top space
-- without a lookup. legacy's is random: any 16 bytes read as a ULID.
INSERT INTO spaces (account_id, id, slug, name, parent_id, created_at)
SELECT id, id, slug, name, NULL, created_at FROM accounts;

INSERT INTO spaces (account_id, id, slug, name, parent_id, created_at)
SELECT a.id, gen_random_uuid(), 'legacy', 'legacy', a.id, a.created_at FROM accounts a;

INSERT INTO space_closure (account_id, ancestor_id, descendant_id, depth)
SELECT account_id, id, id, 0 FROM spaces
UNION ALL
SELECT account_id, parent_id, id, 1 FROM spaces WHERE parent_id IS NOT NULL;
