// Package runtimeconfig records the configuration fixed at a run's first
// successful handover. Record separates the served Configuration from the
// private execution settings its public schema cannot express. New copies
// resolved settings without variables, files, identity or credentials;
// Apply restores them for later handovers. Hook lists are copied so a later
// stack edit or specification cannot mutate the record. The configuration's
// YAML contains the public settings, or the repository's selected source
// projection with redacted environment origins and retained unknown diagnostics.
// ParseFile bounds one repository document, extracts defaults and the matching
// entry, and reports typed key paths and unknown-key notices. Settings and
// Variables apply versioned stack precedence without reconstructing setter intent.
//
// ref: decision 0008 "The run specification and its secrets"
//
// # Input and output
//
// New takes a worker specification and its state-management setting. The
// record is JSON stored by the runs store in the successful specification's transaction.
// Nothing is logged and no secret value is retained.
//
// # Dependencies
//
// It imports workers/api for the execution fields, yaml.v3 for the public
// YAML rendering and the standard library. Handover, lifecycle storage and
// the HTTP adapter use it.
package runtimeconfig
