// Package kek is decision 0013's key encryption key providers, which wrap
// the keyring's data keys. Provider binds a data key's ID into its
// wrapping, so a wrapped key moved to another ID fails to unwrap
// (ErrUnwrap), and Ref names the KEK that wrapped it.
//
// Config is secrets.kek: provider, which New turns into a Provider and
// which is required by every process mode that seals (ErrNoProvider), and
// each provider's settings. The local provider wraps with AES-256-GCM
// through cipher.NewGCMWithRandomNonce under a 32-byte KEK, with the
// associated data infraweave/data-key/<id>, in the format 0x01 | nonce |
// ciphertext and tag. Its KEK is base64 of 32 bytes, in the file
// secrets.kek.local.key_file or in INFRAWEAVE_SECRETS_KEK (Env).
//
// # Input and output
//
// New reads the local key file. Wrap and Unwrap take and return bytes.
// Errors in settings wrap ErrConfig and name the key. Nothing is logged.
//
// # Dependencies
//
// It imports only the standard library. platform/crypt loads the keyring
// through it, and internal/app/config decodes its Config.
package kek
