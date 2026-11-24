-- The links an organization admin opens between two members, which the
-- person who holds both proves by signing in through each.
-- ref: decision 0006 § "Members and identity providers"

-- An open link until expires_at, an hour on, or until the second of its
-- two sign-ins completes it. member_signed_at and other_member_signed_at
-- record each member's sign-in; completed_at the merge of other_member_id
-- into member_id. other_member_id names no member once the link has
-- completed, as the merge deletes that member, so it holds no foreign key
-- and the merge deletes the other links that name it; a link goes with its
-- member_id. A pair has at most one link that has not completed, and a new
-- link of the pair deletes the expired one first.
CREATE TABLE member_links (
    organization_id        uuid        NOT NULL,
    id                     uuid        PRIMARY KEY,
    member_id              uuid        NOT NULL,
    other_member_id        uuid        NOT NULL,
    created_by             text        NOT NULL,
    created_at             timestamptz NOT NULL DEFAULT now(),
    expires_at             timestamptz NOT NULL,
    member_signed_at       timestamptz,
    other_member_signed_at timestamptz,
    completed_at           timestamptz,
    FOREIGN KEY (organization_id, member_id)
        REFERENCES members (organization_id, id) ON DELETE CASCADE,
    CHECK (member_id <> other_member_id),
    CHECK (completed_at IS NULL OR
        (member_signed_at IS NOT NULL AND other_member_signed_at IS NOT NULL))
);
COMMENT ON TABLE member_links IS
    'global: a link merges two members of the organization, whichever accounts admit them';
CREATE UNIQUE INDEX member_links_open ON member_links (organization_id,
    least(member_id, other_member_id), greatest(member_id, other_member_id))
    WHERE completed_at IS NULL;
CREATE INDEX member_links_member ON member_links (organization_id, member_id);
CREATE INDEX member_links_other_member ON member_links (organization_id, other_member_id);
-- authn.sessions.expire's scan for expired links.
CREATE INDEX member_links_expires ON member_links (expires_at) WHERE completed_at IS NULL;
