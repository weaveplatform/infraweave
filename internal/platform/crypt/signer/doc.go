// Package signer holds the private halves of decision 0006's signing keys
// behind one interface, so a token signs the same way whether its key lives
// in process or in a KMS. A Signer gives its public JWK, whose KeyID is the
// kid, and signs a SHA-256 digest, returning the signature in JWS form.
//
// Local is the local backend: GenerateLocal makes an ECDSA P-256 key
// (ES256, the only curve decision 0013's FIPS 140-3 rule allows),
// PrivateKey writes it as PKCS #8 for the caller to seal into
// signing_keys.private_key_sealed, and ParseLocal reads it back, refusing
// any other key type or curve (ErrKey). The KMS backends join behind the
// same interface.
//
// # Input and output
//
// None beyond crypto/rand. It reads no table, file or setting; the access
// domain stores, seals and opens the keys. Nothing is logged.
//
// # Dependencies
//
// It imports the standard library and github.com/go-jose/go-jose/v4 for the
// JWK type. The access domain's token package signs through it.
package signer
