// Package dataview renders docs/implementation/tiers.md, the view of the
// docs/data registers that readers want as tables: every feature of the
// catalogue against the four seeded tiers, with each preview marked, the
// seeded limits, and how many reference rows each phase of the plan owns.
// It also compiles the catalogue into internal/features/catalogue.gen.go:
// every feature with its kind, lowest seeded tier, requirements, limit
// values, covered BillingTierFeature values and preview marker. The
// registers are the only copy of that data, so both files are generated
// (make generate) and drift is caught by make verify-generate rather than
// by review.
//
// Render builds the markdown and Catalogue the Go source from the two
// registers' bytes; Write and WriteCatalogue read them from a directory and
// write the file. Catalogue refuses a requirement or a tier-feature record
// that names no feature, and a preview marker on a limit or the switch,
// without its name, description or documentation URL, or with a URL that
// is not https.
//
// # Input and output
//
// Write and WriteCatalogue read features.json and capabilities.json from
// the data directory they are given (docs/data) and write the file they are
// given (docs/implementation/tiers.md and
// internal/features/catalogue.gen.go). Render and Catalogue do no I/O.
//
// # Dependencies
//
// It imports only the standard library. tools/dataview runs it; like all of
// internal/devtools, nothing linked into cmd/ imports it.
package dataview
