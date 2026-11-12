// Package api holds the acceptance steps of features/api and the answer
// steps every area uses: sending the request a step names through a
// replica, with a token or the browser, and checking the status, problem
// or body it was answered. The api steps drive the served API itself: its
// bundle and reference specification, problems, updates, rate limits and
// disabled callers, and idempotent replays across two replicas.
//
// # Input and output
//
// It sends requests to the scenario's servers through pkg/sdk, seeds and
// reads their database through SQL, and reads the committed bundle and
// reference specification.
//
// # Dependencies
//
// It builds on harness.World. api and kin-openapi load the bundle,
// devtools/openapibundle bundles its sources to compare, and
// devtools/specref regenerates the reference from its sources.
package api
