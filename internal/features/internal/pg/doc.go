// Package pg is the features domain's PostgreSQL storage: Stream, its
// migration stream of decision 0021's tiers, tier features and limit
// values, organization entitlements, the organization's feature and limit
// grants, account limits, the seeded tiers' record and the billing switch,
// with 00003 adding its default tier, NULL for Enterprise; the
// organization and account IDs carry no foreign keys, since no key crosses
// domains (decision 0004). Store creates and deletes an organization's
// and account's rows, reads an entitlement, under FOR UPDATE for a limit
// check, reads the billing switch and every tier, and seeds the tiers a
// release ships, leaving any the operator changed. A new organization gets
// the default tier. For the operator it lists the tiers with how many
// organizations hold each, creates, replaces and deletes a tier, under the
// seed's lock, sets the switch and the default tier, and sets an
// organization's tier and its feature and limit grants; each change bumps
// the entitlement version of every organization it touches. A tier an
// organization holds or the default names cannot be deleted
// (core.ErrInvalidState). Every method joins the transaction its context
// carries.
//
// # Input and output
//
// It reads and writes PostgreSQL through the DB it is given. A seed or an
// operator change sends the payload of what it changed, an organization,
// the tiers or the billing switch, on infraweave_invalidate when it
// commits.
//
// # Dependencies
//
// It imports the catalogue (internal/features) for Tier, featuresapi, the
// domain's features package for the payload, internal/kernel,
// platform/sql, platform/sql/migrate, platform/sql/notify, platform/tx and
// pgx. The features domain's assembly constructs it.
package pg
