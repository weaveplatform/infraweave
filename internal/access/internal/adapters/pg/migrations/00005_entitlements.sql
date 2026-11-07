-- Every organization holds an entitlement (decision 0021). Creating an
-- organization writes its row through featuresapi; an organization
-- created before that holds Enterprise, what it had while billing was
-- off. Every mode seeds Enterprise before it creates an organization, so
-- an installation with organizations already holds the tier. The features
-- stream, which owns the table, runs first wherever both are applied; a
-- database of the access stream alone has no entitlements to give.
DO $$
BEGIN
    IF to_regclass('organization_entitlements') IS NOT NULL THEN
        INSERT INTO organization_entitlements (organization_id, tier)
        SELECT id, 'Enterprise' FROM organizations
        ON CONFLICT (organization_id) DO NOTHING;
    END IF;
END
$$;
