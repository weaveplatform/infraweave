// Package kek is the key encryption key providers, which wrap the keyring's
// data keys. Provider binds a data key's ID into its wrapping, so a wrapped
// key moved to another ID fails to unwrap (ErrUnwrap), and Ref names the
// provider and the KEK that wrapped it.
//
// Config is secrets.kek: provider, which every process mode that seals
// requires (ErrNoProvider), and the settings of local, awskms, gcpkms,
// azurekv and vault, which Validate checks for the provider named. AAD is
// the associated data the providers that take it bind: infraweave/data-key/<id>.
// The remote providers live in the packages named after them; this one
// holds the local provider. OpenLocal reads its KEK, base64 of 32 bytes,
// from secrets.kek.local.key_file or INFRAWEAVE_SECRETS_KEK (Env), and it
// wraps with AES-256-GCM through cipher.NewGCMWithRandomNonce, in the
// format 0x01 | nonce | ciphertext and tag.
//
// ref: decision 0013
//
// # Input and output
//
// OpenLocal reads the local key file. Wrap and Unwrap take and return
// bytes. Errors in settings wrap ErrConfig and name the key. Nothing is
// logged.
//
// # Dependencies
//
// It imports only the standard library. platform/crypt loads the keyring
// through a Provider, internal/app/config decodes its Config, and
// internal/app opens the provider it names.
package kek
