// Package rego embeds bounded OPA compilation and policy evaluation for the
// server and launcher. It imports no business domain.
//
// # Input and output
//
// Policies select supported v0/v1 user-module dialects and a per-type v1
// preamble. Wildcard future keywords retain the original v0/v1 set; a linked
// library's newer dialects do not expand the policy contract. Evaluate
// consumes an independent JSON input and returns only the preamble's raw result
// and bounded captured print text. Typed domain decisions belong to policies.
//
// # Effects
//
// The process caches at most 2,048 prepared queries. Evaluations share eight
// execution slots, honor context deadlines and never send print or input to
// process logs. Worker initialization uses a distinct cache identity and an
// invocation-local redactor before print/message truncation. No input or
// redactor closure enters the cache. Capabilities exclude network, process and nondeterministic
// built-ins; FIPS enforcement removes MD5/SHA-1 built-ins before compilation.
// The worker engine alone adds exec, whose calls go to the Commands each
// worker request carries through its evaluation's context: the prepared query
// holds no executor, a failed call halts the evaluation instead of leaving a
// rule undefined, and a server engine neither compiles exec nor accepts
// Commands, whatever a worker engine cached first.
//
// # Dependencies
//
// The standard library, OPA's v1 ast/rego/topdown APIs and iac/sanitize.
//
// ref: decision 0009
package rego
