-- apiKeyEnableNonAdminManagers (decision 0006): whether admins of spaces
-- below the account's top space may create API keys up to their own
-- access, and a key's creator may delete it whatever their access.
ALTER TABLE accounts ADD COLUMN api_key_non_admin_managers boolean NOT NULL DEFAULT false;
