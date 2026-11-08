-- Other tables hold a stack by its ULID, so a reslug changes one column.
-- space_id has no foreign key because spaces belong to the access domain:
-- a stack holds its space while it is written, and deleting a space asks
-- whether a stack lives in it, which the index answers.
CREATE TABLE stacks (
    account_id uuid   NOT NULL,
    id         uuid   NOT NULL,
    slug       text   NOT NULL,
    space_id   uuid   NOT NULL,
    version    bigint NOT NULL DEFAULT 1,
    PRIMARY KEY (account_id, id),
    UNIQUE (account_id, slug)
);

CREATE INDEX stacks_space ON stacks (account_id, space_id);
