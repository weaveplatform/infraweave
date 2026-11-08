// Package assembly constructs the features domain, the tiers, entitlements
// and limits that gate what an organization may use, at the bottom of the
// declared graph. New builds
// the domain on its Deps; Entitlements is the featuresapi.Entitlements the
// access domain calls; Seed installs the seeded tiers at startup;
// Middleware is the chi middleware internal/app runs after authentication;
// OperatorAPI is its partial of the operator API's strict server, aliased
// FeaturesOperatorAPI, over the Organizations that finds an organization
// by its slug; API is its partial of the API's strict server, aliased
// FeaturesAPI, over the SeatCounter that counts an organization's seats;
// WatchEntitlements drops the replica's snapshots on infraweave_invalidate;
// Stream is its migration stream, which internal/app registers ahead of
// access'; Settings are its features.* settings, features.disabled the
// features no organization of the installation holds, which Validate
// checks against the catalogue.
//
// ref: decision 0021
//
// # Input and output
//
// None of its own: the domain reaches PostgreSQL through Deps and reads
// notifications from the subscription it is given.
//
// # Dependencies
//
// It imports the catalogue (internal/features), featuresapi, the domain's
// features, http and pg packages, internal/kernel, platform/sql/migrate,
// platform/sql/notify and platform/tx. Only internal/app and cmd/ import
// it, as the constructors rule enforces.
package assembly
