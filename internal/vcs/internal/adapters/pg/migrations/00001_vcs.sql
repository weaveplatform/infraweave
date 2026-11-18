-- VCS: each account's integrations with its version control providers.
-- ref: decisions 0004, 0007, 0012 and 0013

-- A stack names an integration by its ULID. slug comes from the name,
-- which never changes, and is unique per provider. space_id has no foreign
-- key because spaces belong to the access domain: an integration holds
-- its space while it is written, and deleting a space asks whether an
-- integration lives in it, which vcs_integrations_space answers. The top
-- space's ID is the account's, so the CHECK keeps a default in the top
-- space, and vcs_integrations_default allows one default per provider.
-- settings are the provider's port.Settings as JSON. credentials_sealed,
-- webhook_secret_sealed and previous_webhook_secret_sealed are sealed to
-- the row's account_id and id, so a value copied to another row fails to
-- open; the previous secret still verifies deliveries until
-- previous_secret_valid_until.
CREATE TABLE vcs_integrations (
    account_id                     uuid        NOT NULL,
    id                             uuid        NOT NULL,
    provider                       text        NOT NULL,
    slug                           text        NOT NULL,
    name                           text        NOT NULL,
    space_id                       uuid        NOT NULL,
    description                    text        NOT NULL DEFAULT '',
    labels                         text[]      NOT NULL DEFAULT '{}',
    is_default                     boolean     NOT NULL DEFAULT false,
    vcs_checks                     text        NOT NULL DEFAULT 'INDIVIDUAL',
    individual_failures_only       boolean     NOT NULL DEFAULT false,
    individual_skip_unchanged      boolean     NOT NULL DEFAULT false,
    use_git_checkout               boolean     NOT NULL DEFAULT false,
    settings                       jsonb       NOT NULL DEFAULT '{}',
    credentials_sealed             bytea,
    webhook_secret_sealed          bytea,
    previous_webhook_secret_sealed bytea,
    previous_secret_valid_until    timestamptz,
    created_at                     timestamptz NOT NULL DEFAULT now(),
    updated_at                     timestamptz NOT NULL DEFAULT now(),
    version                        bigint      NOT NULL DEFAULT 1,
    PRIMARY KEY (account_id, id),
    UNIQUE (id),
    UNIQUE (account_id, provider, slug),
    CONSTRAINT vcs_integrations_default_top CHECK (NOT is_default OR space_id = account_id)
);

CREATE UNIQUE INDEX vcs_integrations_default ON vcs_integrations (account_id, provider)
    WHERE is_default;

CREATE INDEX vcs_integrations_space ON vcs_integrations (account_id, space_id);

-- A GitHub App manifest form an account has opened and not yet completed.
-- id is the form's state, which the App's host hands back with its code;
-- reading the row deletes it, so a state completes once. api_host and
-- user_facing_host are the host the form creates the App on, which the
-- completion converts the code through.
CREATE TABLE vcs_manifests (
    account_id       uuid        NOT NULL,
    id               uuid        NOT NULL,
    api_host         text        NOT NULL,
    user_facing_host text        NOT NULL,
    expires_at       timestamptz NOT NULL,
    PRIMARY KEY (account_id, id)
);

-- A token minted for a run to fetch its stack's source with, until the
-- run ends and a vcs.revoke job revokes it. Only the provider's ID of the
-- token is kept, never the token: revoking needs the ID and the
-- integration's own token, read from integration_id, and the repository
-- the token reads. A token whose integration is deleted first is left to
-- expire at expires_at.
CREATE TABLE vcs_run_tokens (
    account_id     uuid        NOT NULL,
    run_id         uuid        NOT NULL,
    token_id       text        NOT NULL,
    stack_id       uuid        NOT NULL,
    integration_id uuid        NOT NULL,
    provider       text        NOT NULL,
    namespace      text        NOT NULL,
    repository     text        NOT NULL,
    expires_at     timestamptz NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, run_id, token_id)
);

-- Verified deliveries and their jobs commit together. Provider is retained so
-- processing can finish after integration deletion; intake holds the live
-- integration in its insert transaction. Large bodies use unique inbox keys,
-- committed with the row, so a racing duplicate cannot remove the winner.
CREATE TABLE vcs_deliveries (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    integration_id uuid NOT NULL,
    provider text NOT NULL,
    delivery_id text NOT NULL,
    event text NOT NULL,
    received_at timestamptz NOT NULL,
    headers jsonb NOT NULL,
    signature bytea NOT NULL,
    body bytea,
    body_key text,
    repository text NOT NULL,
    state text NOT NULL CHECK (state IN ('received','dispatched','failed','expired')),
    deadline timestamptz NOT NULL,
    replays bigint NOT NULL DEFAULT 0 CHECK (replays >= 0),
    runs_created integer NOT NULL DEFAULT 0 CHECK (runs_created >= 0),
    last_error text NOT NULL DEFAULT '',
    PRIMARY KEY (account_id,id),
    UNIQUE (integration_id,delivery_id),
    CHECK ((body IS NULL) <> (body_key IS NULL)),
    CHECK (body IS NULL OR octet_length(body) <= 262144)
);

CREATE INDEX vcs_deliveries_retention ON vcs_deliveries (received_at,id);
CREATE INDEX vcs_deliveries_received ON vcs_deliveries (id)
    WHERE state='received' OR (state='dispatched' AND last_error='');
CREATE INDEX vcs_deliveries_integration ON vcs_deliveries
    (account_id,integration_id,received_at DESC,id DESC);

-- Requests retain stable repository identity and a path projection so stack
-- reads need no provider call. Head membership is independent of run lifetime.
CREATE TABLE pull_requests (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    integration_id uuid NOT NULL,
    repository_id text NOT NULL,
    namespace text NOT NULL,
    repository text NOT NULL,
    provider_id text NOT NULL,
    base_branch text NOT NULL,
    head_branch text NOT NULL,
    head_sha text NOT NULL,
    author_login text NOT NULL,
    title text NOT NULL,
    url text NOT NULL,
    head_branch_url text NOT NULL,
    draft boolean NOT NULL,
    state text NOT NULL CHECK (state IN ('open','closed','merged')),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,id),
    UNIQUE (integration_id,repository_id,provider_id),
    FOREIGN KEY (account_id,integration_id) REFERENCES vcs_integrations(account_id,id) ON DELETE CASCADE
);
CREATE INDEX pull_requests_integration ON pull_requests (account_id,integration_id);
CREATE INDEX pull_requests_stack_read ON pull_requests
    (account_id,integration_id,namespace,repository,base_branch,updated_at DESC,id DESC) WHERE state='open';
CREATE INDEX pull_requests_retention ON pull_requests (updated_at,id) WHERE state IN ('closed','merged');

CREATE TABLE pull_request_heads (
    account_id uuid NOT NULL,
    request_id uuid NOT NULL,
    sha text NOT NULL,
    became_head_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,request_id,sha),
    FOREIGN KEY (account_id,request_id) REFERENCES pull_requests(account_id,id) ON DELETE CASCADE
);

-- An absent row means no account cap; the operator ceiling still applies.
CREATE TABLE vcs_account_settings (
    account_id uuid PRIMARY KEY,
    event_runs_limit integer NOT NULL CHECK (event_runs_limit >= 1)
);

-- Full diffs are shared by small per-stack job references and expire with their
-- delivery. Event order preserves every ref of a multi-ref provider delivery.
CREATE TABLE vcs_prepared_events (
    account_id uuid NOT NULL,
    delivery_id uuid NOT NULL,
    event_no integer NOT NULL CHECK(event_no >= 0),
    event jsonb NOT NULL,
    PRIMARY KEY(account_id,delivery_id,event_no),
    FOREIGN KEY(account_id,delivery_id) REFERENCES vcs_deliveries(account_id,id) ON DELETE CASCADE
);

-- A stack owns these rows through its deletion guard, not a cross-domain FK.
-- Tags and path ignores for one commit remain independent observations.
CREATE TABLE vcs_ignored_triggers (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    stack_id uuid NOT NULL,
    commit_hash text NOT NULL,
    commit jsonb NOT NULL,
    reason text NOT NULL CHECK(reason IN ('NO_AFFECTED_FILES_IN_PROJECT_ROOT','TAGS_IGNORED_BY_DEFAULT','RUN_LIMIT_REACHED')),
    created_at timestamptz NOT NULL,
    PRIMARY KEY(account_id,id),
    UNIQUE(account_id,stack_id,commit_hash,reason)
);
CREATE INDEX vcs_ignored_triggers_stack ON vcs_ignored_triggers(account_id,stack_id,created_at DESC,id);
CREATE INDEX vcs_ignored_triggers_retention ON vcs_ignored_triggers(created_at,id);

-- Native repository identity survives provider path changes. Paths remain
-- exclusive until an unambiguous event moves the previous occupant. All rows
-- are owned by the integration and disappear with its deletion/account purge.
CREATE TABLE vcs_repository_map (
    account_id uuid NOT NULL,
    integration_id uuid NOT NULL,
    repository_id text NOT NULL,
    namespace text NOT NULL,
    name text NOT NULL,
    owner_id text NOT NULL DEFAULT '',
    observed_at timestamptz NOT NULL,
    PRIMARY KEY (account_id,integration_id,repository_id),
    UNIQUE (account_id,integration_id,namespace,name),
    FOREIGN KEY (account_id,integration_id) REFERENCES vcs_integrations(account_id,id) ON DELETE CASCADE
);
CREATE INDEX vcs_repository_owner ON vcs_repository_map(account_id,integration_id,owner_id);

CREATE TABLE vcs_installations (
    account_id uuid NOT NULL,
    integration_id uuid NOT NULL,
    installation_id text NOT NULL,
    owner_id text NOT NULL,
    owner_login text NOT NULL,
    state text NOT NULL CHECK(state IN ('active','suspended','deleted')),
    is_current boolean NOT NULL DEFAULT true,
    is_superseded boolean NOT NULL DEFAULT false,
    observed_at timestamptz NOT NULL,
    PRIMARY KEY(account_id,integration_id,installation_id),
    FOREIGN KEY(account_id,integration_id) REFERENCES vcs_integrations(account_id,id) ON DELETE CASCADE
);

CREATE UNIQUE INDEX vcs_installation_current_owner ON vcs_installations(account_id,integration_id,owner_id) WHERE is_current;

-- Membership remains after removal drops the current map row, so stack.changed
-- cannot accidentally restore a repository the App no longer reaches.
CREATE TABLE vcs_installation_repositories (
    account_id uuid NOT NULL,
    integration_id uuid NOT NULL,
    installation_id text NOT NULL,
    repository_id text NOT NULL,
    namespace text NOT NULL,
    name text NOT NULL,
    present boolean NOT NULL,
    observed_at timestamptz NOT NULL,
    coordinates_at timestamptz NOT NULL,
    PRIMARY KEY(account_id,integration_id,repository_id),
    FOREIGN KEY(account_id,integration_id,installation_id)
        REFERENCES vcs_installations(account_id,integration_id,installation_id) ON DELETE CASCADE
);
CREATE INDEX vcs_installation_repository_path ON vcs_installation_repositories(account_id,integration_id,namespace,name);
