// Package signer holds the private halves of the token signing keys behind
// one interface, so a token signs the same way whether its key lives in
// process or in a KMS. A Signer gives its public JWK, whose KeyID is the
// kid and whose Algorithm is its JWS alg, and signs a SHA-256 digest,
// returning the signature in JWS form.
//
// The algorithms are ES256 (ECDSA on P-256), RS256 (RSASSA-PKCS1-v1_5)
// and PS256 (RSASSA-PSS with a salt as long as the digest), all over
// SHA-256, since every construction must run in Go's FIPS 140-3 mode.
//
// Local is the local backend: GenerateLocal makes a P-256 key for ES256
// or an RSA key of RSABits (3072) for RS256 and PS256, PrivateKey writes
// it as PKCS #8 for the caller to seal into
// signing_keys.private_key_sealed, and ParseLocal reads it back under its
// algorithm, refusing a key that cannot sign it (ErrKey).
//
// A KMS backend (subpackages awskms, gcpkms, azurekv and vault) is a KMS:
// a Signer whose Ref names its provider and key, as kms_key_ref stores it,
// and whose public JWK has no kid; WithKeyID names it after its
// signing_keys row. The backends share CheckPublic and ParsePKIX, which
// refuse a key that cannot sign the algorithm it is opened for: not P-256
// for ES256, or RSA below 3072 bits for RS256 and PS256 (ErrKey, naming
// the reference); CheckDigest, which also refuses an algorithm outside
// Algorithms (ErrAlgorithm); and FromDER and Raw, which turn the KMS's
// answer into JWS's form or ErrSignature. ErrRef is a reference a backend
// cannot open.
//
// ref: decisions 0006 and 0013
//
// # Input and output
//
// None beyond crypto/rand. It reads no table, file or setting; the access
// domain stores, seals and opens the keys. Nothing is logged.
//
// # Dependencies
//
// It imports the standard library and github.com/go-jose/go-jose/v4 for the
// JWK type. The access domain's token package signs through it, and its
// KMS backends build on it.
package signer
