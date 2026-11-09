-- Configuration: each account's contexts, the config elements contexts and
-- stacks hold, and which stacks each context is attached to.
-- ref: decisions 0004, 0005, 0007 and 0013

-- Other tables hold a context by its ULID; its slug never changes once
-- made. space_id has no foreign key because spaces belong to the access
-- domain: a context holds its space while it is written, and deleting a
-- space asks whether a context lives in it, which contexts_space answers.
-- hooks maps each phase that has commands to them, in order.
CREATE TABLE contexts (
    account_id  uuid        NOT NULL,
    id          uuid        NOT NULL,
    slug        text        NOT NULL,
    space_id    uuid        NOT NULL,
    name        text        NOT NULL,
    description text,
    labels      text[]      NOT NULL DEFAULT '{}',
    hooks       jsonb       NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    version     bigint      NOT NULL DEFAULT 1,
    PRIMARY KEY (account_id, id),
    UNIQUE (account_id, slug)
);

CREATE INDEX contexts_space ON contexts (account_id, space_id);

-- Auto-attachment finds an account's contexts by an autoattach: label.
CREATE INDEX contexts_labels ON contexts USING gin (labels);

-- A config element belongs to a context or to a stack, never both.
-- stack_id has no foreign key because stacks belong to the stacks domain:
-- an element holds its stack while it is written, and deleting the stack
-- deletes the stack's elements through the domain's StackGuard. element
-- is the variable's name or the file's path, the ID the API names it by.
-- value_sealed holds every value, plain and write-only alike, sealed to
-- the row's account_id and id, so a value copied to another row fails to
-- open; checksum is the SHA-256 of the value in hex, which the API
-- answers even when write_only hides the value.
CREATE TABLE config_elements (
    account_id   uuid        NOT NULL,
    id           uuid        NOT NULL,
    context_id   uuid,
    stack_id     uuid,
    element      text        NOT NULL,
    file_mount   boolean     NOT NULL,
    value_sealed bytea       NOT NULL,
    checksum     text        NOT NULL,
    write_only   boolean     NOT NULL,
    description  text        NOT NULL DEFAULT '',
    file_mode    text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, id),
    FOREIGN KEY (account_id, context_id) REFERENCES contexts (account_id, id) ON DELETE CASCADE,
    CHECK ((context_id IS NULL) <> (stack_id IS NULL))
);

CREATE UNIQUE INDEX config_elements_context ON config_elements (account_id, context_id, element)
    WHERE context_id IS NOT NULL;
CREATE UNIQUE INDEX config_elements_stack ON config_elements (account_id, stack_id, element)
    WHERE stack_id IS NOT NULL;

-- A context attaches to a stack at most once. Attachments sort by
-- priority, lowest first; priorities need not be unique. stack_id has no
-- foreign key, as config_elements' has none.
CREATE TABLE context_attachments (
    account_id      uuid        NOT NULL,
    id              uuid        NOT NULL,
    context_id      uuid        NOT NULL,
    stack_id        uuid        NOT NULL,
    priority        integer     NOT NULL DEFAULT 0,
    is_autoattached boolean     NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, id),
    UNIQUE (account_id, context_id, stack_id),
    FOREIGN KEY (account_id, context_id) REFERENCES contexts (account_id, id) ON DELETE CASCADE
);

CREATE INDEX context_attachments_stack ON context_attachments (account_id, stack_id);
