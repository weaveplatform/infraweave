-- Stacks: each account's stacks with the settings their runs read, their
-- hooks, labels and stars.
-- ref: decisions 0004, 0005 and 0007

-- Other tables hold a stack by its ULID, so a reslug changes one column.
-- space_id, worker_pool_id and vcs_integration_id have no foreign keys
-- because spaces, worker pools and VCS integrations belong to other
-- domains: a stack holds its space while it is written, and deleting a
-- space asks whether a stack lives in it, which stacks_space answers.
-- vendor is the vendorConfig member the stack sets and vendor_config that
-- member's settings, as the iac vendor checked them. lock_holder is the
-- principal's caller key, which decides who may unlock without force, and
-- locked_by the name the API shows. tracked_branch_head and tracked_commit
-- are the branch's head and the commit runs use, each a stacksapi.Commit as
-- JSON, which the vcs domain writes; tracked_commit_set_by names who set
-- the tracked commit. state and blocked are the runs domain's projection
-- of the stack's tracked run and of its run lock, which it writes through
-- stacksapi in the transaction that moves the run, so stack lists filter,
-- sort and count by state from this table. Writing any of them changes no
-- version, since no request body carries them. vcs_detached marks a stack
-- whose VCS integration was deleted, which the vcs domain sets, clearing
-- vcs_integration_id, in the delete's transaction; a write that changes
-- the stack's source clears it.
CREATE TABLE stacks (
    account_id                       uuid        NOT NULL,
    id                               uuid        NOT NULL,
    slug                             text        NOT NULL,
    space_id                         uuid        NOT NULL,
    name                             text        NOT NULL,
    description                      text,
    repository                       text        NOT NULL,
    repository_url                   text,
    branch                           text        NOT NULL,
    provider                         text        NOT NULL,
    namespace                        text        NOT NULL DEFAULT '',
    project_root                     text,
    additional_project_globs         text[]      NOT NULL DEFAULT '{}',
    git_sparse_checkout_paths        text[],
    runner_image                     text,
    worker_pool_id                   uuid,
    vcs_integration_id               uuid,
    vcs_detached                     boolean     NOT NULL DEFAULT false,
    vendor                           text        NOT NULL,
    vendor_config                    jsonb       NOT NULL DEFAULT '{}',
    terraform_version                text,
    autodeploy                       boolean     NOT NULL DEFAULT false,
    autoretry                        boolean     NOT NULL DEFAULT false,
    protect_from_deletion            boolean     NOT NULL DEFAULT false,
    disabled                         boolean     NOT NULL DEFAULT false,
    local_preview_enabled            boolean     NOT NULL DEFAULT false,
    github_action_deploy             boolean     NOT NULL DEFAULT true,
    enable_sensitive_output_upload   boolean     NOT NULL DEFAULT false,
    enable_well_known_secret_masking boolean     NOT NULL DEFAULT false,
    skip_vendor_binary_download      boolean     NOT NULL DEFAULT false,
    manages_state                    boolean     NOT NULL DEFAULT false,
    locked_at                        timestamptz,
    lock_holder                      text,
    locked_by                        text,
    lock_note                        text,
    tracked_branch_head              jsonb,
    tracked_commit                   jsonb,
    tracked_commit_set_by            text        NOT NULL DEFAULT '',
    state                            text        NOT NULL DEFAULT 'NONE',
    blocked                          boolean     NOT NULL DEFAULT false,
    created_at                       timestamptz NOT NULL DEFAULT now(),
    version                          bigint      NOT NULL DEFAULT 1,
    PRIMARY KEY (account_id, id),
    UNIQUE (account_id, slug),
    CHECK ((locked_at IS NULL) = (lock_holder IS NULL))
);

CREATE INDEX stacks_space ON stacks (account_id, space_id);
CREATE INDEX stacks_repository ON stacks (account_id, repository, branch);
CREATE INDEX stacks_state ON stacks (account_id, state);

-- One row per hook command, in the order each phase runs them.
CREATE TABLE stack_hooks (
    account_id uuid    NOT NULL,
    stack_id   uuid    NOT NULL,
    phase      text    NOT NULL,
    position   integer NOT NULL,
    command    text    NOT NULL,
    PRIMARY KEY (account_id, stack_id, phase, position),
    FOREIGN KEY (account_id, stack_id) REFERENCES stacks (account_id, id) ON DELETE CASCADE
);

CREATE TABLE stack_labels (
    account_id uuid NOT NULL,
    stack_id   uuid NOT NULL,
    label      text NOT NULL,
    PRIMARY KEY (account_id, stack_id, label),
    FOREIGN KEY (account_id, stack_id) REFERENCES stacks (account_id, id) ON DELETE CASCADE
);

-- Auto-attachment finds an account's stacks by label.
CREATE INDEX stack_labels_label ON stack_labels (account_id, label);

-- A star is one caller's: a session's member or an API key, by its
-- principal kind and actor ID.
CREATE TABLE stack_stars (
    account_id uuid NOT NULL,
    stack_id   uuid NOT NULL,
    actor_kind text NOT NULL,
    actor_id   uuid NOT NULL,
    PRIMARY KEY (account_id, stack_id, actor_kind, actor_id),
    FOREIGN KEY (account_id, stack_id) REFERENCES stacks (account_id, id) ON DELETE CASCADE
);
