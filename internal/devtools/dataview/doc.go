// Package dataview renders docs/implementation/tiers.md, the view of the
// docs/data registers that readers want as tables: every feature of the
// catalogue against the five seeded tiers, the seeded limits, and how many
// reference rows each phase of the plan owns. The registers are the only
// copy of that data, so the view is generated (make generate) and drift is
// caught by make verify-generate rather than by review.
//
// Render builds the markdown from the two registers' bytes; Write reads them
// from a directory and writes the view.
//
// # Input and output
//
// Write reads features.json and capabilities.json from the data directory it
// is given (docs/data) and writes the markdown file it is given
// (docs/implementation/tiers.md). Render does no I/O.
//
// # Dependencies
//
// It imports only the standard library. tools/dataview runs it; like all of
// internal/devtools, nothing linked into cmd/ imports it.
package dataview
