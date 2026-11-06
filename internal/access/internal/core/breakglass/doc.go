// Package breakglass is decision 0006's break-glass password hash:
// PBKDF2-HMAC-SHA256 from crypto/pbkdf2 with Iterations iterations, a
// SaltLen-byte salt and a KeyLen-byte output, written
// $pbkdf2-sha256$i=<iterations>$<salt>$<hash> in unpadded standard base64.
// New hashes a password, which infraweave auth hash-password prints;
// Parse refuses a hash below those parameters with ErrInvalid naming the
// field, so the operator API answers 422; Verify checks a password at the
// break-glass login.
//
// # Input and output
//
// None. New reads crypto/rand.
//
// # Dependencies
//
// The standard library alone. The access domain's assembly, organization
// service and HTTP adapter use it.
package breakglass
