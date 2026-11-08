// Package awskms is the awskms signing backend: it signs the api-token
// purpose's tokens with an AWS KMS key of spec ECC_NIST_P256 and usage
// SIGN_VERIFY, so the private key never leaves KMS. New reads the key's
// public half once, refusing an alias (signer.ErrRef), which may be pointed
// at another key, and a key of another spec or usage (signer.ErrKey), each
// naming the reference. Sign asks KMS to sign the SHA-256 digest with
// ECDSA_SHA_256 and MessageType DIGEST, and converts KMS's DER signature to
// JWS's r || s.
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
