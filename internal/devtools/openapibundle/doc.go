// Package openapibundle merges the spec fragments under api/src/<service>/
// into the one document that oapi-codegen, the server and the parity check
// read. Authors edit fragments so families can be written in parallel
// without one enormous file; the bundle is generated and committed so a
// reader of api/<service>.openapi.yaml sees the whole surface.
//
// The rules match weaveplatform-artifact-registry's bundler, so fragments
// read the same in both repositories: info.yaml holds the document head,
// every other *.yaml holds only paths and components, and nothing may be
// defined twice. Bundle merges one service directory; BundleAll merges every
// one under a root. The result must validate as OpenAPI 3.0.3.
//
// # Input and output
//
// Bundle reads the *.yaml files of one directory and returns the bundle.
// BundleAll also writes each bundle to <outDir>/<service>.openapi.yaml
// (make bundle: api/src to api/). Nothing touches the network.
//
// # Dependencies
//
// It imports yaml.v3 for the fragments and kin-openapi for validation.
// tools/openapi-bundle runs it, and the api/ and acceptance tests call
// Bundle to prove the committed bundle current.
package openapibundle
