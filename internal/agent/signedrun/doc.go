// Package signedrun admits an assignment's run token before the launcher
// uses it: Verifier.Verify checks that the token is the server-signed run
// token of the assigned run, apart from the server that delivered it.
//
// The token must be an ES256 JWT whose kid names a key the server
// answers, whose signature that key verifies, whose iss is the server's
// issuer, whose one aud is infraweave-runner, whose iw_kind is runner,
// whose sub is the assigned run, which carries a jti, and whose iat, nbf
// and exp hold within 30 seconds of the launcher's clock. The token names
// no pool, account or space: the assignment the worker's own session
// polled, and the specification sealed to the pool's key, bind the run to
// its pool. A token that fails is refused with ErrRefused, and keys that
// cannot be read or are no ES256 public keys fail with ErrFailed. Each
// error's text is one fixed reason that names no token, claim value, key
// or server answer, so a run's PREPARING failure note carries it safely.
//
// The keys come only from the worker API of the server the launcher is
// configured for, over the launcher's TLS roots, with its worker session
// token: no token header or claim chooses another issuer or key source.
// They are cached for at most five minutes, and a token naming a kid the
// cache lacks reads them once more, so a key a rotation published and
// promoted is found while an unrelated installation's key is not. Verify
// answers the Trust that admitted the token, the issuer, the audience and
// the keys as a JWK Set document, which an initialization policy may
// verify the token against again.
//
// ref: decision 0006 § "Token kinds", § "Signing keys"
// ref: decision 0009 § "The worker-side initialization policy"
//
// # Input and output
//
// Each read of the keys is one call of its Keys, bounded by 30 seconds and
// any earlier deadline of its caller; the answer is bounded by the
// client. It logs nothing and keeps nothing but the public keys.
//
// # Dependencies
//
// It imports go-jose and its jwt package, internal/agent/wire and the
// standard library. internal/agent/launcher calls it with an
// internal/agent/client.Client as its Keys.
package signedrun
