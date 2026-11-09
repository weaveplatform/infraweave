// Package vendors is the version catalogue: Service refreshes each
// vendor's stored releases from its release index, adding the ones it
// lacks, and logs an index it cannot read, keeping that vendor's stored
// releases as they are; it lists a vendor's stored releases newest first
// and resolves a constraint, in iac/version's grammar, to the newest of
// them it allows. Constraints that do not parse, or that no stored release
// meets, are a *kernel.Invalid naming the constraints parameter. Kinds are
// the vendors it lists.
//
// ref: decisions 0011 and 0022
//
// # Input and output
//
// None of its own: it reads indexes and the store through the ports it is
// given, and logs to the logger a refresh is given.
//
// # Dependencies
//
// It imports internal/iac and iac/version, internal/kernel and the
// domain's index package. The vendors domain's assembly constructs it.
package vendors
