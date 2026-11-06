// Package gcpkms is decision 0013's gcpkms KEK provider: it wraps data keys
// with Cloud KMS Encrypt under the CryptoKey secrets.kek.gcpkms.key_name
// names, with kek.AAD as additional authenticated data, and unwraps them
// with Decrypt. Every request carries CRC32C checksums, and a response
// whose integrity fields do not confirm them is ErrIntegrity. Cloud KMS's
// INVALID_ARGUMENT on Decrypt is kek.ErrUnwrap; any other error names the
// provider and the key as itself.
//
// # Input and output
//
// Wrap and Unwrap each call Cloud KMS once over REST, with Application
// Default Credentials, so the recorded fixtures in testdata serve the same
// transport. Close closes the client. Nothing is logged.
//
// # Dependencies
//
// It imports platform/crypt/kek and cloud.google.com/go/kms. internal/app
// opens it.
package gcpkms
