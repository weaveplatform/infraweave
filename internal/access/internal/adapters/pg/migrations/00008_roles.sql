-- Roles and role bindings: each account's roles, the actions of its
-- custom roles, and the grants of a role to an actor in a space. Creating
-- an account seeds its system roles.
-- ref: decision 0007

-- A system role's actions are computed from the action catalogue and
-- never stored, so role_actions holds only custom roles' and a catalogue
-- refresh changes every account's system roles at once.
CREATE TABLE roles (
    account_id  uuid        NOT NULL REFERENCES accounts ON DELETE RESTRICT,
    id          uuid        PRIMARY KEY,
    slug        text        NOT NULL,
    name        text        NOT NULL,
    description text        NOT NULL DEFAULT '',
    is_system   boolean     NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    version     bigint      NOT NULL DEFAULT 1,
    UNIQUE (account_id, slug),
    UNIQUE (account_id, id)
);

CREATE TABLE role_actions (
    account_id uuid NOT NULL REFERENCES accounts ON DELETE RESTRICT,
    role_id    uuid NOT NULL,
    action     text NOT NULL,
    PRIMARY KEY (role_id, action),
    FOREIGN KEY (account_id, role_id) REFERENCES roles (account_id, id) ON DELETE CASCADE
);

CREATE INDEX role_actions_role ON role_actions (account_id, role_id);

-- A role with bindings is not deleted until its bindings are, so the
-- role link restricts; a binding goes with its space. The unique key's index, led by the actor, serves the reads of
-- one actor's bindings.
CREATE TABLE role_bindings (
    account_id uuid        NOT NULL REFERENCES accounts ON DELETE RESTRICT,
    id         uuid        PRIMARY KEY,
    actor_kind text        NOT NULL
        CHECK (actor_kind IN ('user', 'group', 'api_key', 'stack', 'flows_project')),
    actor_id   uuid        NOT NULL,
    role_id    uuid        NOT NULL,
    space_id   uuid        NOT NULL,
    origin     text        NOT NULL CHECK (origin IN ('binding', 'access_rule')),
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by text        NOT NULL,
    version    bigint      NOT NULL DEFAULT 1,
    UNIQUE (actor_kind, actor_id, role_id, space_id),
    FOREIGN KEY (account_id, role_id) REFERENCES roles (account_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (account_id, space_id) REFERENCES spaces (account_id, id) ON DELETE CASCADE
);

CREATE INDEX role_bindings_role ON role_bindings (account_id, role_id);
CREATE INDEX role_bindings_space ON role_bindings (account_id, space_id);
