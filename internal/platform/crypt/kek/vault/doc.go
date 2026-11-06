// Package vault is decision 0013's vault KEK provider: it wraps data keys
// with the Transit engine at secrets.kek.vault.mount under the key
// key_name names, binding kek.AAD as associated_data, which a key of an
// AEAD type checks on decrypt. Transit's 400 on decrypt is kek.ErrUnwrap;
// any other answer, a refused permission among them, names the provider
// and the key as itself.
//
// It gets its token by auth.method: token reads auth.token_file or
// VAULT_TOKEN (TokenEnv) once; kubernetes and approle log in at the first
// call, reading auth.jwt_file or auth.secret_id_file then, log in again
// halfway through the token's lease, and once more when Vault refuses the
// token.
//
// # Input and output
//
// Wrap and Unwrap POST JSON to Vault's HTTP API, logging in first when
// they need a token. New reads the token file or variable for the token
// method. Nothing is logged.
//
// # Dependencies
//
// It imports platform/crypt/kek and the standard library: it calls the
// Transit API over net/http rather than through Vault's client module.
// internal/app opens it.
package vault
