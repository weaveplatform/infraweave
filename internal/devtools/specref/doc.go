// Package specref turns each upstream Spacelift product's API into a
// resource-oriented REST OpenAPI 3 reference for infraweave, and measures the
// served API against it.
//
// A Product is one product's reference: its directory, its generated
// document, its parity baseline, and how its sources become the document.
// Deploy is projected from a GraphQL schema, the spacelift-api-bruno
// collection and the Spacelift Terraform provider. Flows has no public
// schema; it is projected from the RPC calls its two MIT-licensed clients
// make, terraform-provider-flows and flowctl. Each product keeps its own
// sources.lock.yaml, document title and coverage text. Product.Generate,
// GenerateDir and CheckDir build, write and check a product's document and
// coverage.md, Pins reads its lock's pins, and Inventory writes or checks its
// inventory register.
//
// For Deploy, Fetch snapshots the upstream sources at the pins of
// sources.lock.yaml, and ReadSources, BuildPlan and BuildOpenAPI map every
// GraphQL root field to a REST route by the rules of report 02 and
// rest-mapping.yaml. Its inventory is report 29 and the GraphQL inventory
// register.
//
// For Flows, FetchFlows snapshots the provider's CallFlowsAPI calls
// (ReadFlowsProviderTarball), flowctl's apiFetch calls and type declarations
// (ReadFlowctlAPI) and the app SDK's identifiers (ReadSDKIdentifiers), and
// validates every published flow template and an app registry against the
// hand-written schemas.yaml (ValidateFlowDefinition). ReadFlowsSources and
// GenerateFlows project each RPC path to a REST route by report 02's verb
// rules and rest-mapping.yaml, failing on a path that is neither mapped nor
// excluded and on a mapping no client calls. Its inventory is the SDK
// identifier register.
//
// For any product, ScaffoldFile copies one reference tag into a
// served-API fragment with decision 0003's conventions, and Product.Parity
// and ParityCheck compare the served operations under the product's own tags
// with its reference against the accepted baseline.
//
// # Input and output
//
// Only Fetch and FetchFlows touch the network, and only Fetch runs a
// program. Fetch posts the introspection query to the GraphQL endpoint it is
// given (tools/specref defaults to demo.app.spacelift.io), downloads the
// Bruno collection and provider tarballs from a codeload base URL
// (codeload.github.com by default), identifying itself by its User-Agent, and
// runs terraform in a temporary directory to dump the provider schema; it
// then writes the
// snapshots and the lock under the reference directory
// (api/reference/spacelift). FetchFlows downloads the provider and flowctl
// tarballs from the same codeload base, the SDK tarball from an npm registry
// (registry.npmjs.org), the template index (docs.spacelift.io) and an app
// registry's manifest and one app's versions file, following no URL off the
// registry's own host; it writes the snapshots and the lock under
// api/reference/spacelift-flows. Everything else reads a reference directory
// and writes: the document and coverage report (GenerateDir), a fragment
// under api/src/api (ScaffoldFile), report 29 and
// docs/data/graphql-inventory.json or docs/data/flows-sdk-inventory.json
// (Product.Inventory), and the parity baseline (WriteParityBaseline). The
// check functions write nothing.
//
// # Dependencies
//
// It imports kin-openapi to build and validate the document, yaml.v3, and
// go/ast to read the providers' Go source; flowctl's TypeScript and the SDK's
// declarations are read by its own small lexer and parser. The standard
// library serves the rest. tools/specref runs it, and the api/ and
// acceptance tests call its parity and reference checks.
package specref
