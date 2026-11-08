-- Decision 0007's roles and role bindings: each account's roles, the
-- actions of its custom roles, and the grants of a role to an actor in a
-- space.

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

-- A role with bindings is not deleted until its bindings are (decision
-- 0007 answers 409), so the role link restricts; a binding goes with its
-- space. The unique key's index, led by the actor, serves the reads of
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
    UNIQUE (actor_kind, actor_id, role_id, space_id),
    FOREIGN KEY (account_id, role_id) REFERENCES roles (account_id, id) ON DELETE RESTRICT,
    FOREIGN KEY (account_id, space_id) REFERENCES spaces (account_id, id) ON DELETE CASCADE
);

CREATE INDEX role_bindings_role ON role_bindings (account_id, role_id);
CREATE INDEX role_bindings_space ON role_bindings (account_id, space_id);

-- Every account that exists before this migration gets the system roles
-- account creation seeds, with the slugs, names and descriptions of
-- core/role's Seed. No binding is inferred for an account claimed before
-- it.
INSERT INTO roles (account_id, id, slug, name, description, is_system)
SELECT a.id, gen_random_uuid(), r.slug, r.name, r.description, true
FROM accounts a CROSS JOIN (VALUES
    ('space-reader', 'Space reader', 'Every assignable action the reader level grants.'),
    ('space-writer', 'Space writer', 'Every assignable action the writer level grants.'),
    ('space-admin', 'Space admin', 'Every assignable action.'),
    ('worker-pool-controller', 'Worker pool controller',
        'Reads the space and manages its worker pools.'),
    ('drift-detection-manager', 'Drift detection manager',
        'Reads the space and manages its drift detection integrations.')
) AS r (slug, name, description);
