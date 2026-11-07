-- The tier a new organization gets (decision 0021). NULL is Enterprise,
-- which the seed installs after the migrations run, so the billing row
-- that exists before any tier cannot name it.
ALTER TABLE billing ADD COLUMN default_tier text REFERENCES tiers ON DELETE RESTRICT;
CREATE INDEX billing_default_tier ON billing (default_tier);
