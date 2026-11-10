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
