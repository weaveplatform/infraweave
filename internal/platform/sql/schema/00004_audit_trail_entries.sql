-- The audit trail: an account's entries, each recorded in the transaction
-- of the change it describes. Entries with an organization and no account
-- belong to organization_audit_trail_entries, so account_id is never null
-- here. No foreign key to accounts: the platform stream references no
-- domain.
-- ref: decision 0015

CREATE TABLE audit_trail_entries (
    account_id        uuid        NOT NULL,
    id                uuid        NOT NULL,
    actor_username    text        NOT NULL,
    actor_run         uuid,
    action            text        NOT NULL,
    event_type        text        NOT NULL,
    affected_type     text        NOT NULL,
    affected_id       text        NOT NULL,
    affected_metadata jsonb,
    related_type      text,
    related_id        text,
    related_metadata  jsonb,
    body              jsonb,
    remote_ip         text,
    identity_ulid     uuid,
    owner_login       text,
    created_at        timestamptz NOT NULL,
    PRIMARY KEY (account_id, id),
    CHECK ((related_type IS NULL) = (related_id IS NULL)),
    CHECK (related_type IS NOT NULL OR related_metadata IS NULL)
);

-- The search pages and the ordered shipper read an account's entries in
-- (created_at, id) order.
CREATE INDEX audit_trail_entries_created ON audit_trail_entries (account_id, created_at, id);
