-- Vendors: the releases each IaC vendor's release index lists, which the
-- version catalogue answers from. They are the same for every account, so
-- no row names one.
-- ref: decisions 0004 and 0011

-- vendor is the iac kind, such as terraform. published_at is when the
-- index first said the release was published, or null when the index
-- gives no date, as OpenTofu's does. A release is never rewritten once
-- stored, so a refresh that finds nothing new writes nothing.
CREATE TABLE vendor_releases (
    vendor       text        NOT NULL,
    version      text        NOT NULL,
    published_at timestamptz,
    PRIMARY KEY (vendor, version)
);
COMMENT ON TABLE vendor_releases IS
    'global: a vendor''s releases are the same for every account';
