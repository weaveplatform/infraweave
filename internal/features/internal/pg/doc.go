// Package pg is the features domain's PostgreSQL storage: Stream, its
// migration stream of decision 0021's tiers, tier features and limit
// values, organization entitlements, the organization's feature and limit
// grants, account limits, the seeded tiers' record and the billing switch;
// the organization and account IDs carry no foreign keys because the
// domain sits below access. Store creates and deletes an organization's
// and account's rows, reads an entitlement, under FOR UPDATE for a limit
// check, reads the billing switch and every tier, and seeds the tiers a
// release ships, leaving any the operator changed. Every method joins the
// transaction its context carries.
//
// # Input and output
//
// It reads and writes PostgreSQL through the DB it is given, and a seed
// that changes a tier sends the domain's tiers payload on
// infraweave_invalidate when it commits.
//
// # Dependencies
//
// It imports the catalogue (internal/features) for Tier, featuresapi, the
// domain's features package for the payload, internal/kernel,
// platform/sql, platform/sql/migrate, platform/sql/notify, platform/tx and
// pgx. The features domain's assembly constructs it.
package pg
