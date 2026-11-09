// Package http is the vendors domain's HTTP adapter: Server, the domain's
// partial of the API's strict server, serves the Version Catalog's
// terraformVersions, openTofuVersions, terraformEffectiveVersion and
// openTofuEffectiveVersion over vendorsapi.Catalog. A versions list pages
// newest first, each cursor naming the last version of its page and the
// vendor's list, so a cursor of one vendor's list is refused by the
// other's as invalid-cursor. An effective version the catalogue refuses
// is answered as the *kernel.Invalid it returns.
//
// # Input and output
//
// None beyond the requests it answers.
//
// # Dependencies
//
// It imports vendorsapi, internal/iac and iac/version, internal/kernel,
// platform/gen/api, platform/httpkit/page and platform/httpkit/problem.
// The vendors domain's assembly constructs it.
package http
