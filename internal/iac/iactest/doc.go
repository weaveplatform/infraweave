// Package iactest is the contract suite every vendor under internal/iac
// passes. Run fails a test with each of Problems, which holds a vendor to
// the contract's cases and to the vendor's own: it is registered under its
// Kind; each Case refuses exactly the fields it names, each refusal with a
// reason clients branch on, a message and a pointer into the settings;
// and ValidateSettings changes no input and answers the same from many
// goroutines at once as it does alone.
//
// ref: decision 0016
//
// # Input and output
//
// None. It calls the vendor it is given in memory.
//
// # Dependencies
//
// It imports the standard library, internal/iac and internal/kernel. Each
// vendor's tests import it.
package iactest
