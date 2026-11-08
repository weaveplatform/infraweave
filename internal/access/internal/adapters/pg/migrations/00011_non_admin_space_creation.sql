-- allowNonAdminSpaceCreation: whether admins of spaces below the
-- account's top space may create, move and delete spaces below the spaces
-- they administer. While it is off they change only the name, description
-- and labels of those spaces.
ALTER TABLE accounts ADD COLUMN allow_non_admin_space_creation boolean NOT NULL DEFAULT false;
