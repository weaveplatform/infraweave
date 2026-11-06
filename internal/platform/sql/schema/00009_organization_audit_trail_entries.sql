-- Decision 0015's organization trail: entries with an organization and no
-- account, such as the operator API's changes to an organization, so
-- audit_trail_entries stays account-scoped. Like that table it has no
-- foreign key: the platform stream references no domain, and an
-- organization's deletion is itself an entry that outlives it.
CREATE TABLE organization_audit_trail_entries (
    organization_id uuid        NOT NULL,
    id              uuid        NOT NULL,
    -- The principal kind that acted, such as operator or break_glass, and
    -- its actor ID, so an entry names an actor with no username.
    actor_kind      text        NOT NULL,
    actor_id        text        NOT NULL,
    action          text        NOT NULL,
    event_type      text        NOT NULL,
    affected_type   text        NOT NULL,
    affected_id     text        NOT NULL,
    body            jsonb,
    remote_ip       text,
    created_at      timestamptz NOT NULL,
    PRIMARY KEY (organization_id, id)
);

-- Organization admins list an organization's entries in (created_at, id)
-- order, as an account's are.
CREATE INDEX organization_audit_trail_entries_created
    ON organization_audit_trail_entries (organization_id, created_at, id);

COMMENT ON TABLE organization_audit_trail_entries IS
    'global: an organization''s own trail, above any account';
