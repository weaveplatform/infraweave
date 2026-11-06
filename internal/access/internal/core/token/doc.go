// Package token issues and verifies decision 0006's API-facing JWTs and
// rotates the keys that sign them.
//
// Service.Issue signs a Request with the api-token purpose's active key
// (ES256, through go-jose and a platform/crypt/signer Signer), creating the
// first key when there is none. The claims are exactly iss, aud as one
// string, sub, jti (the session ULID), iat, nbf equal to iat, exp and
// iw_kind (Kind: api_key, or operator for the operator API's token).
// Service.Verify accepts ES256 alone, so alg none and
// every HMAC algorithm fail, checks the signature against the key the kid
// names, and requires the issuer, exactly the one audience, the claims
// above and iat, nbf and exp within Leeway of now; every refusal wraps
// ErrToken. Public keys are cached per replica, and an unknown kid reloads
// them at most once a second; a replica rereads which key is active every
// SignerRefresh.
//
// Keys pass through StateNext, StateActive, StateRetiring and
// StateRetired. Policy.Plan is the rotation due at a time: a next key
// KeyLifetime minus NextLead into the active key's life, a promotion once
// the active key is KeyLifetime old and the next key has verified for
// MinPublished, and retirement RetireGrace after the last token a retiring
// key can have signed expires. Manual rotation creates the first key and
// replaces none. Service.Rotate is authn.keys.rotate.
//
// # Input and output
//
// None of its own: keys come from a Store, which applies a rotation's
// steps in one transaction, and the clock is the caller's. It logs nothing.
//
// # Dependencies
//
// It imports github.com/go-jose/go-jose/v4 and its jwt package,
// internal/kernel and platform/crypt/signer. The access domain's pg
// adapter implements Store, and its assembly constructs the Service.
package token
