// Package loopback is the server's half of the CLI's loopback login
// (decision 0006's CLI token, spacectl's browser handshake): Seal
// encrypts a CLI token to the RSA public key the CLI generated, so the
// browser that carries it to the CLI's local listener never reads it.
// The envelope is spacectl's: an AES-256 key wrapped with RSA-OAEP over
// SHA-512, and the token in AES-CBC with a leading IV and PKCS #7
// padding, each in unpadded base64url, joined by a dot. Every primitive
// is one FIPS 140-3 approves. A key that is not RSA of at least
// MinKeyBits is ErrKey.
//
// # Input and output
//
// None. Seal reads crypto/rand.
//
// # Dependencies
//
// The standard library alone. The access domain's authn service uses it.
package loopback
