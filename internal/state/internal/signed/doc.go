// Package signed signs and verifies the tokens of the state domain's
// signed URLs: an Upload token, which lets its bearer PUT one state file
// for import, and a Download token, which lets its bearer GET one state
// version. A Token names its account, the principal that asked for it,
// its target and its expiry, and is signed with HMAC-SHA-256 under a key
// derived for its kind, so one kind never verifies as the other.
//
// # Input and output
//
// None; it encodes and decodes tokens in memory, deriving their keys
// through Keys.
//
// # Dependencies
//
// It imports internal/kernel and the standard library's crypto. The state
// domain's assembly constructs a Signer over the installation's
// crypt.Keyring.
package signed
