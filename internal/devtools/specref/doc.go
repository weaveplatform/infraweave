// Package specref turns the upstream Spacelift API (a GraphQL schema, the
// spacelift-api-bruno collection and the Spacelift Terraform provider) into a
// resource-oriented REST OpenAPI 3 reference for infraweave, and measures the
// served API against it.
//
// Fetch snapshots the upstream sources at the pins of sources.lock.yaml.
// ReadSources, BuildPlan and BuildOpenAPI map every GraphQL root field to a
// REST route by the rules of report 02 and rest-mapping.yaml; Generate and
// GenerateDir produce spacelift.openapi.yaml and coverage.md, and CheckDir
// fails when the committed files differ. Scaffold copies one reference tag
// into a served-API fragment with decision 0003's conventions. Inventory
// renders report 29 and the GraphQL inventory register. CompareParity and
// ParityCheck compare the served document with the reference against the
// accepted baseline.
//
// # Input and output
//
// Only Fetch touches the network or runs a program. It posts the
// introspection query to the GraphQL endpoint it is given (tools/specref
// defaults to demo.app.spacelift.io), downloads the Bruno collection and
// provider tarballs from a codeload base URL (codeload.github.com by
// default), identifying itself by its User-Agent, and runs terraform in a temporary directory to dump
// the provider schema; it then writes the snapshots and the lock under the
// reference directory (api/reference/spacelift). Everything else reads those
// snapshots and writes: the reference and coverage report (generate), a
// fragment under api/src/api (scaffold), report 29 and
// docs/data/graphql-inventory.json (inventory), and the parity baseline
// (parity --write-baseline). The check modes write nothing.
//
// # Dependencies
//
// It imports kin-openapi to build and validate the document, yaml.v3, and
// go/ast to read the provider's Go source; the standard library otherwise.
// tools/specref runs it, and the api/ and acceptance tests call its parity
// and reference checks.
package specref
