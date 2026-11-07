// Package assembly constructs the features domain, decision 0021's tiers,
// entitlement and limits, at the bottom of the declared graph. New builds
// the domain on its Deps; Entitlements is the featuresapi.Entitlements the
// access domain calls; Seed installs the seeded tiers at startup;
// Middleware is the chi middleware internal/app runs after authentication;
// WatchEntitlements drops the replica's snapshots on infraweave_invalidate;
// Stream is its migration stream, which internal/app registers ahead of
// access'; Settings are its features.* settings, features.disabled the
// features no organization of the installation holds, which Validate
// checks against the catalogue.
//
// # Input and output
//
// None of its own: the domain reaches PostgreSQL through Deps and reads
// notifications from the subscription it is given.
//
// # Dependencies
//
// It imports the catalogue (internal/features), featuresapi, the domain's
// features, http and pg packages, platform/sql/migrate,
// platform/sql/notify and platform/tx. Only internal/app and cmd/ import
// it, as the constructors rule enforces.
package assembly
