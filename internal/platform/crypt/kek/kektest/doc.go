// Package kektest is the contract every KEK provider passes: Contract
// wraps and unwraps a data key, and checks that a wrapped key moved to
// another data key ID, or changed, fails to unwrap with kek.ErrUnwrap.
//
// # Input and output
//
// Contract calls the provider it is given and reports through testing.T.
// It reads and writes nothing else.
//
// # Dependencies
//
// It imports platform/crypt/kek. Only the providers' tests import it.
package kektest
