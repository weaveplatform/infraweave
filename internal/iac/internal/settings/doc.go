// Package settings holds the checks the vendors under internal/iac share:
// New reads a vendor's member of a stack input's vendorConfig as a JSON
// object, and a Checker's Bool, Version, Enum and Workspace refuse a
// field of the wrong type, a version constraint that does not parse, a
// value outside an enum, and a workspace name a URL path would escape;
// Unsupported refuses a value the input schema allows that infraweave
// does not run. Errors are the refusals, each a kernel.FieldError whose
// pointer is relative to the settings object.
//
// # Input and output
//
// None. It decodes JSON in memory.
//
// # Dependencies
//
// It imports the standard library, internal/kernel for the field errors
// and iac/version for the constraint syntax. Only the vendors under
// internal/iac import it.
package settings
