// Package azurekv is the azurekv signing backend: it signs the api-token
// purpose's tokens with one version of a Key Vault key of type EC or EC-HSM
// on P-256, so the private key never leaves Key Vault. The reference is the
// version's key ID, https://<vault>/keys/<name>/<version>, since a key's
// current version changes when it rotates; anything else is signer.ErrRef,
// and a key of another type or curve signer.ErrKey, each naming the
// reference. Sign asks Key Vault to sign the SHA-256 digest with ES256,
// whose answer is already JWS's r || s.
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
