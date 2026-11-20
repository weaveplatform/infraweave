// Package rego embeds bounded OPA compilation and policy evaluation for the
// server and launcher. It imports no business domain.
//
// # Input and output
//
// Policies select their user-module dialect and per-type v1 preamble. Evaluate
// consumes an independent JSON input and returns only the preamble's raw result
// and bounded captured print text. Typed domain decisions belong to policies.
//
// # Effects
//
// The process caches at most 2,048 prepared queries. Evaluations share eight
// execution slots, honor context deadlines and never send print or input to
// process logs. Capabilities exclude network, process and nondeterministic
// built-ins; FIPS enforcement removes MD5/SHA-1 built-ins before compilation.
//
// # Dependencies
//
// The standard library, OPA's v1 ast/rego/topdown APIs and iac/sanitize.
//
// ref: decision 0009
package rego
