-- The account-wide settings no other domain owns. An account without a row
-- reads every default, so creating an account writes none; the first
-- change writes it.
CREATE TABLE account_settings (
    account_id                 uuid        PRIMARY KEY REFERENCES accounts ON DELETE RESTRICT,
    personal_api_keys          boolean     NOT NULL DEFAULT true,
    -- Whether API keys may manage API keys; only a member may turn it on.
    api_key_non_human_managers boolean     NOT NULL DEFAULT false,
    security_email             text        NOT NULL DEFAULT '',
    -- The OAuth redirect allowlist: bare hosts and hosts with one leading
    -- "*.", in the order an admin set them.
    oauth_redirect_hosts       text[]      NOT NULL DEFAULT '{}',
    survey_completed           boolean     NOT NULL DEFAULT false,
    survey_response            text,
    -- The deletion mark: an account may be deleted while marked_at is set,
    -- with no waiting period.
    marked_at                  timestamptz,
    mark_reason                text,
    mark_description           text,
    CHECK (marked_at IS NOT NULL OR (mark_reason IS NULL AND mark_description IS NULL))
);
