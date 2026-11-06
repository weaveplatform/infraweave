// Package apikey is decision 0006's API keys as the token exchange sees
// them. A secret is Prefix, iwk_, and 43 base64url characters encoding 32
// random bytes (NewSecret); only its SHA-256 is stored (Hash), and Matches
// compares digests in constant time. Usable is whether a key may exchange:
// neither disabled, deleted nor expired.
//
// # Input and output
//
// None. NewSecret reads crypto/rand; nothing else touches state.
//
// # Dependencies
//
// It imports the standard library and internal/kernel. The access
// domain's authn service and pg adapter use it.
package apikey
