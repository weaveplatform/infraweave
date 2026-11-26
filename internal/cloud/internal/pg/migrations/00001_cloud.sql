-- Cloud: each account's AWS and Azure integrations and the stacks they attach to.
-- ref: decisions 0004, 0006 and 0013

-- An integration lives in a space and names the role runs assume. space_id
-- has no foreign key because spaces belong to the access domain: an
-- integration holds its space while it is written, and deleting a space
-- asks whether an integration lives in it, which aws_integrations_space
-- answers. role_arn and external_id are plain text: neither grants
-- anything without the trust policy of the role. external_id is the
-- custom external ID a private worker assumes the role with, empty when
-- the generated one applies. legacy marks an integration bound to one
-- stack, which the stack's legacy AWS role made.
CREATE TABLE aws_integrations (
    account_id                     uuid        NOT NULL,
    id                             uuid        NOT NULL,
    space_id                       uuid        NOT NULL,
    name                           text        NOT NULL,
    role_arn                       text        NOT NULL,
    region                         text,
    duration_seconds               integer     NOT NULL,
    generate_credentials_in_worker boolean     NOT NULL DEFAULT false,
    external_id                    text        NOT NULL DEFAULT '',
    labels                         text[]      NOT NULL DEFAULT '{}',
    autoattach_enabled             boolean     NOT NULL DEFAULT false,
    tag_assume_role                boolean     NOT NULL DEFAULT false,
    legacy                         boolean     NOT NULL DEFAULT false,
    created_at                     timestamptz NOT NULL DEFAULT now(),
    updated_at                     timestamptz NOT NULL DEFAULT now(),
    version                        bigint      NOT NULL DEFAULT 1,
    PRIMARY KEY (account_id, id),
    CONSTRAINT aws_integrations_name UNIQUE (account_id, name)
);

CREATE INDEX aws_integrations_space ON aws_integrations (account_id, space_id);

-- Auto-attachment finds an account's integrations by an autoattach label.
CREATE INDEX aws_integrations_labels ON aws_integrations USING gin (labels);

-- An attachment serves a stack's read phases, its write phases or both.
-- stack_id has no foreign key because stacks belong to the stacks domain:
-- an attachment holds its stack while it is written, and deleting the
-- stack deletes its attachments through the domain's StackGuard. The
-- partial unique indexes keep a stack at one read and one write
-- attachment, whichever integrations serve them.
CREATE TABLE aws_integration_attachments (
    account_id     uuid        NOT NULL,
    id             uuid        NOT NULL,
    integration_id uuid        NOT NULL,
    stack_id       uuid        NOT NULL,
    read           boolean     NOT NULL,
    write          boolean     NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, id),
    CONSTRAINT aws_integration_attachments_pair UNIQUE (account_id, integration_id, stack_id),
    FOREIGN KEY (account_id, integration_id) REFERENCES aws_integrations (account_id, id)
        ON DELETE CASCADE,
    CHECK (read OR write)
);

CREATE UNIQUE INDEX aws_integration_attachments_read
    ON aws_integration_attachments (account_id, stack_id) WHERE read;
CREATE UNIQUE INDEX aws_integration_attachments_write
    ON aws_integration_attachments (account_id, stack_id) WHERE write;

-- An Azure integration lives in a space and names the multi-tenant Entra
-- application the installation created for a customer's tenant. space_id
-- has no foreign key, as for aws_integrations. tenant_id, application_id
-- and object_id are plain text: none grants anything without the
-- application's credential and the tenant's consent. tenant_id never
-- changes after creation. service_principal_object_id is NULL until the
-- application first gets credentials for the customer's tenant.
-- admin_consent_provided is an admin's statement, never checked against
-- Entra; consent_actor and consent_at are who made the latest statement,
-- or cleared it, and when. An integration with autoattach_enabled has a
-- default_subscription_id, which an automatic attachment uses.
CREATE TABLE azure_integrations (
    account_id                  uuid        NOT NULL,
    id                          uuid        NOT NULL,
    space_id                    uuid        NOT NULL,
    name                        text        NOT NULL,
    tenant_id                   text        NOT NULL,
    application_id              text        NOT NULL,
    object_id                   text        NOT NULL,
    display_name                text        NOT NULL,
    service_principal_object_id text,
    default_subscription_id     text,
    labels                      text[]      NOT NULL DEFAULT '{}',
    autoattach_enabled          boolean     NOT NULL DEFAULT false,
    admin_consent_provided      boolean     NOT NULL DEFAULT false,
    consent_actor               text,
    consent_at                  timestamptz,
    created_at                  timestamptz NOT NULL DEFAULT now(),
    updated_at                  timestamptz NOT NULL DEFAULT now(),
    version                     bigint      NOT NULL DEFAULT 1,
    PRIMARY KEY (account_id, id),
    CONSTRAINT azure_integrations_name UNIQUE (account_id, name),
    CHECK (NOT autoattach_enabled OR default_subscription_id IS NOT NULL)
);

CREATE INDEX azure_integrations_space ON azure_integrations (account_id, space_id);

CREATE INDEX azure_integrations_labels ON azure_integrations USING gin (labels);

-- An Azure attachment serves a stack's read phases, its write phases or
-- both, with subscription_id overriding its integration's
-- default_subscription_id when set. stack_id has no foreign key, as for
-- aws_integration_attachments, and the partial unique indexes keep a
-- stack at one read and one write Azure attachment.
CREATE TABLE azure_integration_attachments (
    account_id      uuid        NOT NULL,
    id              uuid        NOT NULL,
    integration_id  uuid        NOT NULL,
    stack_id        uuid        NOT NULL,
    read            boolean     NOT NULL,
    write           boolean     NOT NULL,
    subscription_id text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, id),
    CONSTRAINT azure_integration_attachments_pair UNIQUE (account_id, integration_id, stack_id),
    FOREIGN KEY (account_id, integration_id) REFERENCES azure_integrations (account_id, id)
        ON DELETE CASCADE,
    CHECK (read OR write)
);

CREATE UNIQUE INDEX azure_integration_attachments_read
    ON azure_integration_attachments (account_id, stack_id) WHERE read;
CREATE UNIQUE INDEX azure_integration_attachments_write
    ON azure_integration_attachments (account_id, stack_id) WHERE write;
