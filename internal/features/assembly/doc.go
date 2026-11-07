// Package assembly constructs the features domain, decision 0021's tiers,
// entitlement and limits, at the bottom of the declared graph. New builds
// the domain on its Deps; Entitlements is the featuresapi.Entitlements the
// access domain calls; Stream is its migration stream, which internal/app
// registers ahead of access'; Settings are its features.* settings,
// features.disabled the features no organization of the installation
// holds.
//
// # Input and output
//
// None of its own: the domain reaches PostgreSQL through Deps.
//
// # Dependencies
//
// It imports featuresapi, the domain's features and pg packages,
// platform/sql/migrate and platform/tx. Only internal/app and cmd/ import
// it, as the constructors rule enforces.
package assembly
