// Package gcpkms is the gcpkms signing backend: it signs tokens with a
// Cloud KMS CryptoKeyVersion, so the private key never leaves Cloud KMS.
// An api-token key, or a federation key under ES256, has algorithm
// EC_SIGN_P256_SHA256; a federation key under RS256 has
// RSA_SIGN_PKCS1_3072_SHA256 or RSA_SIGN_PKCS1_4096_SHA256, and under
// PS256 RSA_SIGN_PSS_3072_SHA256 or RSA_SIGN_PSS_4096_SHA256. The
// reference is the version's resource name, since each version's key pair
// is fixed; anything else is signer.ErrRef, and a version whose algorithm
// does not sign the one it is opened for signer.ErrKey, each naming the
// reference. Sign sends the SHA-256 digest with its CRC32C, checks the
// answer's integrity fields (kek/gcpkms.ErrIntegrity otherwise), and
// converts an ECDSA signature from DER to JWS's r || s.
//
// ref: decision 0006
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
