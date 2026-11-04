// Package specref turns each upstream Spacelift product's API into a
// resource-oriented REST OpenAPI 3 reference for infraweave, and measures the
// served API against it.
//
// A Product is one product's reference: its directory, its generated
// document, its parity baseline, and how its sources become the document.
// Deploy is the one product so far, projected from a GraphQL schema, the
// spacelift-api-bruno collection and the Spacelift Terraform provider; each
// product keeps its own sources.lock.yaml, document title and coverage text.
// Product.Generate, GenerateDir and CheckDir build, write and check a
// product's document and coverage.md, and Pins reads its lock's pins.
//
// For Deploy, Fetch snapshots the upstream sources at the pins of
// sources.lock.yaml, and ReadSources, BuildPlan and BuildOpenAPI map every
// GraphQL root field to a REST route by the rules of report 02 and
// rest-mapping.yaml. Inventory renders report 29 and the GraphQL inventory
// register. For any product, ScaffoldFile copies one reference tag into a
// served-API fragment with decision 0003's conventions, and CompareParity and
// ParityCheck compare the served document with the reference against the
// accepted baseline.
//
// # Input and output
//
// Only Fetch touches the network or runs a program. It posts the
// introspection query to the GraphQL endpoint it is given (tools/specref
// defaults to demo.app.spacelift.io), downloads the Bruno collection and
// provider tarballs from a codeload base URL (codeload.github.com by
// default), identifying itself by its User-Agent, and runs terraform in a
// temporary directory to dump the provider schema; it then writes the
// snapshots and the lock under the reference directory
// (api/reference/spacelift). Everything else reads a reference directory and
// writes: the document and coverage report (GenerateDir), a fragment under
// api/src/api (ScaffoldFile), report 29 and docs/data/graphql-inventory.json
// (InventoryFile), and the parity baseline (WriteParityBaseline). The check
// functions write nothing.
//
// # Dependencies
//
// It imports kin-openapi to build and validate the document, yaml.v3, and
// go/ast to read the provider's Go source; the standard library otherwise.
// tools/specref runs it, and the api/ and acceptance tests call its parity
// and reference checks.
package specref
