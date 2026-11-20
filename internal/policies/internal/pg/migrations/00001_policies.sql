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
