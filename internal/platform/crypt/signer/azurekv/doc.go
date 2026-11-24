// Package azurekv is the azurekv signing backend: it signs tokens with one
// version of a Key Vault key, so the private key never leaves Key Vault.
// An api-token key, or a federation key under ES256, is of type EC or
// EC-HSM on P-256; a federation key under RS256 or PS256 is of type RSA or
// RSA-HSM of at least 3072 bits. The reference is the version's key ID,
// https://<vault>/keys/<name>/<version>, since a key's current version
// changes when it rotates; anything else is signer.ErrRef, and a key that
// cannot sign the algorithm it is opened for signer.ErrKey, each naming
// the reference. Sign asks Key Vault to sign the SHA-256 digest with
// ES256, RS256 or PS256, whose answer is already in JWS's form.
//
// ref: decision 0006
//
// # Input and output
//
// New calls Key Vault's getKey once and Sign calls sign, through the
// client the azurekv KEK provider builds, with azidentity's default chain.
// Nothing is logged.
//
// # Dependencies
//
// It imports platform/crypt/signer, platform/crypt/kek/azurekv for the
// client, and the Azure SDK's azkeys module. internal/app opens it.
package azurekv
