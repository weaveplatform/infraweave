// Package catalogue loads the pinned MIT policy example library and adapts its
// parsed namespaces for public templates and compatibility tests. The embedded
// original sources, identities, license and digest inventory are immutable.
//
// # Input and output
//
// Get resolves a captured opaque ID; All returns independent template metadata
// slices. Rewrite takes pinned Rego source and returns its adapted body and
// namespace-change count while preserving arbitrary strings. Original LOGIN and
// legacy ACCESS sources remain available to the compatibility corpus.
//
// # Effects
//
// Loading reads embedded files and caches immutable data in memory. No database,
// network, worker or external policy action is invoked.
//
// # Dependencies
//
// The standard library, OPA's AST/formatter, yaml.v3 and the platform Rego dialect
// types. Consumers use the production runtime to compile or evaluate templates.
//
// ref: decision 0009
package catalogue
