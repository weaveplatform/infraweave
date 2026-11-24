// Package auth holds the acceptance steps of features/auth: the token
// exchange and API sessions, API keys and personal keys, browser sign-in
// and its sessions, break-glass sign-in, the organization's identity
// providers, the OAuth server, signing tokens with a KMS key, and the
// federation issuer's discovery document and JWKS, read with no
// credential and through go-oidc.
//
// # Input and output
//
// It sends requests to the scenario's servers through pkg/sdk and the
// browser's cookie jar, seeds and reads their database through SQL,
// creates Vault Transit keys for KMS signing, and opens a signing key's
// sealed private half under the scenario's keyring to sign a token a
// relying party verifies.
//
// # Dependencies
//
// It builds on harness.World and harness.Jar, test/deps for Vault and the
// waits on another replica, kernel for the IDs it seeds, platform/crypt
// for a sealed column, go-jose to sign and go-oidc as a relying party.
package auth
