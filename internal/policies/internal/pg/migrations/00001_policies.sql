-- Policies hold spaces through access' API rather than cross-domain foreign keys.
CREATE TABLE policies (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    space_id uuid NOT NULL,
    name text NOT NULL,
    body text NOT NULL,
    description text,
    labels text[] NOT NULL DEFAULT '{}',
    type text NOT NULL,
    engine_type text NOT NULL,
    template_origin text,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    PRIMARY KEY (account_id, id)
);
CREATE INDEX policies_space ON policies (account_id, space_id);
CREATE INDEX policies_labels ON policies USING gin (labels);

-- Effective attachments have their own identity. A manual attachment replaces
-- the automatic marker without replacing the identity or duplicating selection.
CREATE TABLE policy_stack_attachments (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    policy_id uuid NOT NULL,
    stack_id uuid NOT NULL,
    is_autoattached boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id, id),
    UNIQUE (account_id, policy_id, stack_id),
    FOREIGN KEY (account_id, policy_id) REFERENCES policies (account_id, id) ON DELETE CASCADE
);
CREATE INDEX policy_stack_attachments_stack ON policy_stack_attachments (account_id, stack_id);

-- Input objects have immutable keys. Upload staging is encrypted to its row;
-- publication joins the owning evaluation/capture transaction, never a run write.
CREATE TABLE policy_input_objects (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    object_key text NOT NULL UNIQUE,
    state text NOT NULL CHECK (state IN ('pending','uploading','live','failed','expired','superseded')),
    staging_sealed bytea,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    upload_started_at timestamptz,
    published_at timestamptz,
    PRIMARY KEY (account_id, id),
    CHECK (staging_sealed IS NULL OR state IN ('pending','uploading'))
);
CREATE INDEX policy_input_objects_expiry ON policy_input_objects (expires_at, account_id, id)
    WHERE state IN ('pending','uploading','live');

CREATE TABLE policy_evaluations (
    account_id uuid NOT NULL,
    id uuid NOT NULL,
    policy_id uuid NOT NULL,
    policy_name text NOT NULL,
    policy_version bigint NOT NULL,
    space_id uuid NOT NULL,
    type text NOT NULL,
    engine_type text NOT NULL,
    outcome text NOT NULL,
    target_kind text NOT NULL,
    target_id uuid NOT NULL,
    run_id uuid,
    sample_requested boolean NOT NULL,
    sample_input_id uuid,
    blob_key text,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (account_id, id),
    FOREIGN KEY (account_id, sample_input_id) REFERENCES policy_input_objects (account_id, id),
    CHECK ((sample_input_id IS NULL) = (blob_key IS NULL))
);
CREATE INDEX policy_evaluations_policy ON policy_evaluations (account_id, policy_id, created_at DESC, id DESC);
CREATE INDEX policy_evaluations_run ON policy_evaluations (account_id, run_id) WHERE run_id IS NOT NULL;
CREATE INDEX policy_evaluations_input ON policy_evaluations (account_id, sample_input_id);

CREATE TABLE policy_sample_budgets (
    account_id uuid NOT NULL,
    hour timestamptz NOT NULL,
    bucket text NOT NULL CHECK (bucket IN ('NOTIFICATION','GIT_PUSH','OTHER')),
    used integer NOT NULL CHECK (used > 0 AND used <= 500),
    PRIMARY KEY (account_id, hour, bucket)
);
