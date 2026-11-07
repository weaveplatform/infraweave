// Package oauth is decision 0006's authorization server for MCP clients:
// public clients, the authorization code with PKCE S256 alone, and
// rotating refresh tokens, where each grant is a session of the member in
// one account.
//
// The rules stand alone. Scopes reads a scope parameter, where mcp:write
// brings mcp:read and an empty one is mcp:read. CheckRedirectURI is
// registration's rule, loopback http or https; SameRedirect matches an
// authorization request's redirect URI to a registered one, a loopback
// URI on any port; AllowedRedirect is consent's, loopback or a host of
// the account's oauthRedirectAllowedHosts. Resource names the MCP
// surfaces: /mcp, /flows/mcp and each flow's server below it.
// CheckChallenge and VerifyPKCE are RFC 7636's S256.
//
// Service serves the protocol. Metadata is the RFC 8414 document;
// Register is RFC 7591's registration; Authorize stores a request for
// RequestTTL and answers the console's consent page for it, or the
// client's redirect URI with an error once that URI is known good.
// ConsentRequest, Approve and Deny are the consent page's: the member
// approves for an account that admits them, and the code they issue
// lives auth.tokens.oauth_code.ttl. Token redeems a code once, and a
// refresh token once: each refresh spends the token, and a spent one
// presented again revokes the whole grant, as a code redeemed twice
// does. An access token is a JWT of iw_kind oauth whose aud is the
// grant's resource and whose scope is the grant's, or narrower on a
// refresh; its sub is the member's login. Every refusal of the protocol
// is an *Error carrying an OAuth error code. Expire deletes requests a
// day after their end and clients unused for ClientRetention.
//
// # Input and output
//
// None of its own: Service reads and writes through Store, signs through
// Issuer, and reads crypto/rand for codes and refresh tokens.
//
// # Dependencies
//
// It imports the standard library, internal/kernel and the access
// domain's port, session and token packages. The access domain's HTTP
// adapter, pg adapter and assembly use it.
package oauth
