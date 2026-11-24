// Package vault is the vault signing backend: it signs tokens with one
// version of a key of Vault's Transit engine, so the private key never
// leaves Vault. An api-token key, or a federation key under ES256, is of
// type ecdsa-p256; a federation key under RS256 or PS256 is of type
// rsa-3072 or rsa-4096, signing with pkcs1v15 or with pss and a salt as
// long as the digest. It reaches Vault at secrets.kek.vault's address
// with its auth method, through the vault KEK provider's client, and needs
// none of that provider's Transit key. The reference is
// <mount>/<key name>/<version>, since a Transit key's latest version
// changes when it rotates; anything else is signer.ErrRef, and a key whose
// type cannot sign the algorithm it is opened for, or without that
// version, signer.ErrKey, each naming the reference. Sign sends the
// SHA-256 digest prehashed, with jws marshaling, so Transit answers the
// signature in JWS's form.
//
// ref: decision 0006
//
// # Input and output
//
// New reads <mount>/keys/<name> once and Sign posts to
// <mount>/sign/<name>, logging in first when the auth method needs a
// token. Nothing is logged.
//
// # Dependencies
//
// It imports platform/crypt/signer and platform/crypt/kek/vault for the
// client. internal/app opens it.
package vault
