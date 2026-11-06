// Package awskms is decision 0013's awskms KEK provider: it wraps data keys
// with AWS KMS Encrypt under the key secrets.kek.awskms.key_id names, by ID,
// ARN or alias, with the encryption context
// {"infraweave":"data-key","id":"<id>"}, and unwraps them with Decrypt,
// naming the key so a blob another key wrapped is refused. KMS's
// InvalidCiphertextException and IncorrectKeyException are kek.ErrUnwrap;
// any other error, a refused permission among them, names the provider
// and the key as itself.
//
// # Input and output
//
// Wrap and Unwrap each call KMS once over HTTPS, with credentials from the
// AWS SDK's default chain and the region secrets.kek.awskms.region names or
// the SDK's. Nothing is logged.
//
// # Dependencies
//
// It imports platform/crypt/kek and aws-sdk-go-v2's config and kms.
// internal/app opens it; the awskms signing backend builds its client with
// NewClient.
package awskms
