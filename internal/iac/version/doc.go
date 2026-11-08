// Package version parses the version constraints Terraform and OpenTofu
// stacks name: ParseConstraints reads a field such as ">= 1.5, < 2.0" or
// "~> 1.6.0" into Constraints, each an Op and a Version, and Parse reads
// one Version. A constraint that does not parse is ErrSyntax, with an error
// that says which part. Which release a constraint resolves to is the
// Version Catalog's answer, not this package's.
//
// # Input and output
//
// None. It parses strings in memory.
//
// # Dependencies
//
// It imports the standard library only. The terraform and opentofu
// vendors import it, and the Version Catalog may.
package version
