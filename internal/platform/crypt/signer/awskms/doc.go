// Package awskms is the awskms signing backend: it signs tokens with an
// AWS KMS key of usage SIGN_VERIFY, so the private key never leaves KMS.
// An api-token key, or a federation key under ES256, is of spec
// ECC_NIST_P256 and signs with ECDSA_SHA_256; a federation key under RS256
// or PS256 is of spec RSA_3072 or RSA_4096 and signs with
// RSASSA_PKCS1_V1_5_SHA_256 or RSASSA_PSS_SHA_256. New reads the key's
// public half once, refusing an alias (signer.ErrRef), which may be
// pointed at another key, and a key that cannot sign the algorithm it is
// opened for (signer.ErrKey), each naming the reference: one whose usage
// or signing algorithms do not include it, or whose public key is not
// P-256 for ES256 or RSA of at least 3072 bits for RS256 and PS256. Sign asks KMS to sign the SHA-256 digest with MessageType
// DIGEST, and converts an ECDSA signature from DER to JWS's r || s.
//
// ref: decision 0006
//
// # Input and output
//
// New calls KMS GetPublicKey and Sign calls KMS Sign, through the client
// the awskms KEK provider builds, with credentials from the AWS SDK's
// default chain. Nothing is logged.
//
// # Dependencies
//
// It imports platform/crypt/signer, platform/crypt/kek/awskms for the
// client, and the AWS SDK's KMS module. internal/app opens it.
package awskms
