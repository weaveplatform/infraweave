// Package gcpkms is decision 0006's gcpkms signing backend: it signs the
// api-token purpose's tokens with a Cloud KMS CryptoKeyVersion of
// algorithm EC_SIGN_P256_SHA256, so the private key never leaves Cloud
// KMS. The reference is the version's resource name, since each version's
// key pair is fixed; anything else is signer.ErrRef, and a version of
// another algorithm signer.ErrKey, each naming the reference. Sign sends
// the SHA-256 digest with its CRC32C, checks the answer's integrity fields
// (kek/gcpkms.ErrIntegrity otherwise), and converts the DER signature to
// JWS's r || s.
//
// # Input and output
//
// New calls GetPublicKey once and Sign calls AsymmetricSign, over the
// REST client the gcpkms KEK provider builds, with Application Default
// Credentials. Nothing is logged.
//
// # Dependencies
//
// It imports platform/crypt/signer, platform/crypt/kek/gcpkms for the
// client and its checksum, and Google's Cloud KMS module. internal/app
// opens it.
package gcpkms
