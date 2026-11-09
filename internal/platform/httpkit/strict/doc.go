// Package strict is what every domain's HTTP adapter shares to serve an
// update route the way the API's conventions say: PUT replaces, PATCH
// applies an RFC 7396 merge patch through the PUT, a read carries the
// entity's version as a strong ETag, and a write that carries If-Match is
// refused when it names no version the entity has now.
//
// Merge applies a merge patch to a decoded JSON document; Patch applies
// one to an input view, the read model projected onto the update input.
// Apply runs a PATCH: it reads the view and version, merges, and writes
// through the PUT's path expecting the version it read, reading again when
// another write wins the race and the client sent no If-Match. ETag is a
// version's entity tag, and IfMatch reads an If-Match header as the
// kernel.Expect a domain's write checks inside its transaction, where
// kernel.ErrStale is the refusal problem answers with 412.
//
// ref: decision 0022
//
// # Input and output
//
// None beyond its arguments: it transforms values in memory, and Apply
// calls the functions it is given.
//
// # Dependencies
//
// It imports the standard library and internal/kernel. Each domain's HTTP
// adapter imports it for its update routes.
package strict
