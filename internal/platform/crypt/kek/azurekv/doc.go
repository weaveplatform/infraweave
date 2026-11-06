// Package azurekv is decision 0013's azurekv KEK provider: it wraps data
// keys with Key Vault wrapKey and RSA-OAEP-256 under the RSA key
// secrets.kek.azurekv.key_name names in the vault vault_url names, at
// key_version or the key's current version. RSA-OAEP takes no associated
// data, so the provider wraps the data key's ID ahead of the key and
// unwrapKey's answer must start with the ID asked for. A wrapped key is
// 0x01 | version length | key version | ciphertext, so a key rotated in
// Key Vault still unwraps what its older version wrapped. Key Vault's 400
// on unwrapKey, and an ID other than the one asked for, are kek.ErrUnwrap;
// any other error names the provider and the key as itself.
//
// # Input and output
//
// Wrap and Unwrap each call Key Vault once over HTTPS, with credentials
// from azidentity's default chain. Nothing is logged.
//
// # Dependencies
//
// It imports platform/crypt/kek, azcore, azidentity and azkeys.
// internal/app opens it; the azurekv signing backend builds its client with
// NewClient.
package azurekv
