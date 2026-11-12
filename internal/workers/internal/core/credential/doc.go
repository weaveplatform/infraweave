// Package credential is the cryptography of a private worker pool's
// credentials: the installation's pool CA (NewCA, ParseCA, Marshal), the
// check of a pool's certificate signing request, which must be for an EC
// P-256 key (ParseCSR), the certificate the CA signs for a pool's key
// with a serial (Sign, Verify), and the pool token that carries it
// (Token, NewToken, Encode, DecodeToken).
//
// # Input and output
//
// None; it computes over the values it is given and reads crypto/rand.
//
// # Dependencies
//
// It imports internal/kernel and the standard library's crypto and
// encoding packages.
package credential
