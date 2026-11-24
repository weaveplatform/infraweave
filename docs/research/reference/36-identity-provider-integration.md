# 36: Identity provider integration libraries and test identity providers

Checked against: `github.com/coreos/go-oidc/v3` v3.21.0, `golang.org/x/oauth2` v0.38.0, `github.com/go-jose/go-jose/v4` v4.1.9, `github.com/zitadel/oidc/v3` v3.53.0, `github.com/crewjam/saml` v0.6.3, `github.com/russellhaering/goxmldsig` v1.6.1, `github.com/russellhaering/gosaml2` v0.12.0, `github.com/elimity-com/scim` at `5fffe914c211`, `github.com/go-webauthn/webauthn` v0.18.2, `github.com/descope/virtualwebauthn` v1.0.5, Dex v2.46.1, navikt mock-oauth2-server 7.0.1, Keycloak 26.8.2, the Go 1.27.4 standard library source, and the public discovery documents and JWKS of Microsoft Entra ID, Auth0, Google and GitHub Actions read 2026-11-24.

Research baseline: **2026-11-24**.

This report records the facts behind the relying-party, SAML service-provider,
SCIM, WebAuthn and GitHub OAuth choices of
[decision 0006](../decisions/0006-authentication-and-tokens.md#human-login)
and the identity-provider test doubles of
[decision 0016](../decisions/0016-testing-and-acceptance.md). It is facts only;
the decisions hold the choices.

## 1. The certified FIPS mode decides several choices

infraweave builds with `GOFIPS140=certified` and runs its unit tests a second
time under `GODEBUG=fips140=only`. Read from the Go 1.27.4 source, that mode
refuses or panics on these operations: [S1]

| Operation under `fips140=only` | Result |
|---|---|
| `crypto/sha1` `Sum`, or `Sum` on a SHA-1 hash | Panics; `Write` returns an error |
| `cipher.NewGCM` | Returns an error, for decryption too |
| CFB mode | Panics |
| AES-CBC and AES-CTR with an AES block | Allowed |
| RSA-OAEP with SHA-1 or SHA-1 MGF1; PKCS #1 v1.5 encryption | Returns an error |
| RSA keys under 2048 bits, odd sizes, or an exponent of 2^16 or less | Refused |
| Plain Ed25519 | Allowed; Ed25519ctx is refused |
| HMAC | Allowed with a key of 112 bits or more |

### 1.1 Parsing a JWKS that carries `x5c`

go-jose v4.1.9's `JSONWebKey.UnmarshalJSON` calls `sha1.Sum` over the leaf
certificate whenever a key carries `x5c`, whether or not it also carries
`x5t`. Under `fips140=only` the parse panics. [S2][S3]

| Issuer | Publishes `x5c` or `x5t` in its JWKS |
|---|---|
| Microsoft Entra ID | Yes |
| Auth0 | Yes |
| GitHub Actions | Yes |
| Google | No |

The JWKS of each issuer above was read on 2026-11-24. [S4] The OIDC API-key verifier in
[`internal/access/internal/adapters/oidc`](../../../internal/access/internal/adapters/oidc/oidc.go)
parses with go-jose, so an OIDC API key from GitHub Actions fails under
`fips140=only` today; net/http recovers the panic and drops the request. A
library that fetches keys in a background goroutine, as go-oidc's
`RemoteKeySet` and zitadel's key set do, would crash the process instead.
Removing `x5c`, `x5t` and `x5t#S256` from each key before go-jose parses it
avoids the hash; a key fetched over TLS from the issuer's own JWKS URI loses
nothing by it. An upstream change makes the thumbprint check conditional and
is still open. [S3]

## 2. OIDC relying party

| | `x/oauth2` and go-oidc v3 | zitadel/oidc v3 `rp` | Written on go-jose v4 |
|---|---|---|---|
| Release | v3.21.0, 2026-09-01; the v3 API is unchanged since 2021 | v3.53.0, 2026-11-23; a v4 prerelease is out, so an import path change is coming | go-jose v4.1.9, 2026-11-05 |
| PKCE | `oauth2.GenerateVerifier`, `S256ChallengeOption`, `VerifierOption` | `WithPKCE`, the verifier kept in a cookie | As column 1 |
| Nonce | `oidc.Nonce` sends it; `Verify` does not check it | `WithNonce` | To write |
| `auth_time` and `max_age` | Not checked; read through `Claims` | `WithAuthTimeMaxAge` | To write |
| ID token checks | `iss`, `aud`, `exp`, an algorithm allow-list, `at_hash`; an empty `sub` is accepted (issue 495) | `iss`, `aud`, `azp`, `iat`, `exp`, algorithm | The API-key verifier checks `iss`, `aud`, `exp` and `nbf` with leeway and an algorithm allow-list |
| Key set | `RemoteKeySet` has no TTL, refetches on any failed verification with no rate limit, in a background goroutine; `oidc.NewVerifier(issuer, keySet, config)` takes any `KeySet` | A TTL with a 10 second backoff, in a background goroutine | The repository's: a one-hour cache, an unknown `kid` refetched at most once a minute, HTTPS only, documents capped at 1 MiB |
| Discovery | `NewProvider`, with the HTTP client from `ClientContext` | `NewRelyingPartyOIDC` | The repository's, with an exact `issuer` match |
| Logout | `VerifyLogout` verifies back-channel logout tokens (v3.19.0); the end-session endpoint is read through `Provider.Claims` | `EndSession`; no back-channel verification | To write |
| New modules | None: go-jose v4 and `x/oauth2` are already required | About ten, among them OpenTelemetry, securecookie, a schema decoder and CORS | None |
| Licence | Apache-2.0 and BSD-3-Clause | Apache-2.0 | Apache-2.0 |

[S5][S6][S7][S8]

go-oidc with a `KeySet` backed by the repository's own key cache keeps one
HTTPS-only, size-capped, FIPS-safe fetch path for API keys and sign-in, and
adds no module. The checks go-oidc leaves to its caller are the nonce, a
non-empty `sub`, `azp` when the token has more than one audience, `auth_time`
against `max_age`, and `email_verified`. [S5]

## 3. OIDC provider conventions

### 3.1 Logout

| Mechanism | Specification | Notes |
|---|---|---|
| RP-initiated | `end_session_endpoint` with `id_token_hint` and `post_logout_redirect_uri` | Advertised in discovery where supported [S9] |
| Back-channel | A signed logout token posted to the relying party, naming `sid` or `sub`, with a single-use `jti` | Auth0 advertises `backchannel_logout_supported`; Entra ID advertises only front-channel; Google advertises neither [S4][S10] |
| Front-channel | An iframe per relying party | Depends on third-party cookies, which browsers block |

### 3.2 Re-authentication

OIDC Core section 3.1.2.1 makes `auth_time` required in the ID token when the
request carried `max_age`, and leaves the age check to the relying party.
Support for `max_age` varies by provider, so the relying party enforces it.
`amr` and `acr` values differ from provider to provider and say nothing
comparable about a second factor. [S9][S11]

### 3.3 `email_verified`

Google, Okta, Auth0, Dex and Keycloak emit `email_verified`. Entra ID does not
list it in `claims_supported`, and its `email` claim can be changed by the
user's tenant; the optional `xms_edov` claim reports domain ownership when an
administrator turns it on. [S4][S12]

### 3.4 Groups

| Provider | Groups in the ID token |
|---|---|
| Okta | Not by default; an administrator adds a groups claim, usually named `groups`, with a filter; values are group names [S13] |
| Microsoft Entra ID | `groupMembershipClaims`; values are object IDs by default, names only for groups synchronised from on-premises; above 200 groups the claim is replaced by `_claim_names` and `_claim_sources` pointing at Microsoft Graph [S14] |
| Google Workspace | None; `hd` names the hosted domain, and groups need the Cloud Identity or Admin SDK APIs [S11] |
| Auth0 | Not by default; an Action adds a custom claim, recommended under a namespaced name [S15] |

The claim's name and its values therefore differ per provider, and an absent
claim is common.

## 4. SAML service provider

| | crewjam/saml v0.6.3 | gosaml2 v0.12.0 |
|---|---|---|
| Release | v0.6.0 to v0.6.3 on 2026-11-07 and 08; a new `x/samlsp` and `x/samlidp` whose API may still change | 2026-08 |
| Advisories | About 17, 12 of them published 2026-11-07, including a multi-reference signature bypass in goxmldsig fixed with goxmldsig 1.6.0 or later | 9, 5 of them in 2026 |
| SHA-1 | `x/samlsp` refuses SHA-1 signature and digest methods by default; goxmldsig itself has no algorithm allow-list | No switch |
| Replay | Each assertion accepted once, keyed by a SHA-256 of the issuer and message ID; the default store is per process and replaceable with `WithReplayState` | No cache |
| Request binding | `WithRequestID` and `WithExpectedIssuer`; an unsolicited response is refused unless `AllowUnsolicited` is set | Not tracked |
| Metadata | `RemoteMetadata` refreshes hourly, HTTPS only unless signers are set, with a size cap and an SSRF guard that `WithHTTPClient` replaces; static XML through `ParseMetadata` | Static only |
| NameID formats | All four; transient by default; `SubjectKey` refuses a transient NameID | All |
| Clock skew | `WithClockSkew`, `WithMaxIssueDelay`, `WithClock` | `ClockSkew` |
| New modules | crewjam/saml, beevik/etree, goxmldsig, `code.dny.dev/ssrf`, clockwork | etree, goxmldsig |
| Licence | BSD-2-Clause | Apache-2.0 |

[S16][S17][S18][S19]

Encrypted assertions use AES-GCM by default, which `fips140=only` refuses, and
Entra ID and Okta wrap the key with `rsa-oaep-mgf1p`, whose SHA-1 the mode also
refuses. AES-CBC works but is refused by the default hardening in an unsigned
response. A service provider that publishes no encryption key in its metadata
receives no encrypted assertion. [S1][S16]

Metadata aggregates that list more than one identity provider have led to the
first one being trusted (GHSA-fvc9); refusing an aggregate avoids it. [S17]

### 4.1 Test identity provider

| Option | Fit |
|---|---|
| crewjam/saml `x/samlidp`, in the test process | Signs the response and the assertion with SHA-256 by default; the application supplies the signed-in subject, so a test chooses the NameID format and attributes; encrypts only when the service provider's metadata has an encryption key |
| The legacy `samlidp` package | Has its own user store and login form |
| `kristophjunge/test-saml-idp` | SimpleSAMLphp 1.15.2, last updated about nine years ago |
| Keycloak | Maintained, heavy, and its login is a form to scrape |
| BoxyHQ mock-saml | Signs in by e-mail only, with no documented NameID or attribute control |

[S16][S20][S21]

## 5. SCIM v2 server

| | elimity-com/scim | scim2/filter-parser v2 alone |
|---|---|---|
| Release | No tags; pseudo-versions, the latest 2026-11-13; the README calls it early stage | v2.3.1, 2026-07 |
| Shape | An `http.Handler`, mounted under chi with `http.StripPrefix`; handlers take the `*http.Request`, so the router's authentication middleware and context apply | A parser only |
| Served | `/Schemas`, `/ServiceProviderConfig`, `/ResourceTypes`, schema validation, `POST /.search`; filters as a parsed AST; `PATCH` as parsed operations | Everything else written by hand |
| Not served | ETags, bulk, sort | |
| Licence | MIT | MIT |

[S22][S23]

Identity providers send a small set of filters: `userName`, `externalId`,
`displayName` and `id` with `eq`, and `members[value eq "…"]` paths. Entra ID
sends capitalised operations such as `"Replace"`, `"active":"False"` as a
string, and `PATCH` value maps with no path. [S22][S24]

Microsoft's SCIM validator runs in a browser against a publicly reachable
endpoint. `scim2-tester`, through `scim2-cli`, runs from a command line and
covers discovery, create, read, update, delete and `PATCH`. [S24][S25]

## 6. WebAuthn

`go-webauthn/webauthn` v0.18.2 (2026-09-19) verifies ES256, RS256 and EdDSA
assertions with the standard library, all three allowed by the FIPS module
for RSA keys of 2048 bits or more; RS1 hashes with SHA-1. `WithCredentialParameters(CredentialParametersRecommendedL3())`
limits credential algorithms to EdDSA, ES256 and RS256, and registration
refuses any other. Attestation formats cannot be limited at verification:
`WithAttestationFormats` is a hint to the client, and a `packed` or `tpm`
statement may carry its own algorithm. The zero values of the attestation and
signature settings are the most restrictive. [S26]

`descope/virtualwebauthn` v1.0.5 produces `packed` attestations and assertions
with P-256 or RSA-2048 keys and SHA-256, so it runs under `fips140=only`; it
has no Ed25519 key and no `none` format. Chrome's DevTools protocol offers
virtual authenticators, which Playwright reaches through a CDP session. [S27][S28]

## 7. GitHub OAuth sign-in

`golang.org/x/oauth2/github` provides only the authorize, token and device
endpoints of github.com. GitHub Enterprise Server serves `/login/oauth/…` at
its host root, not under `/api/v3`. The user, e-mail, team and organization
membership reads are `go-github` calls, and `go-github` is already required.
[S29][S30] The repository's
[`internal/vcs/githubfake`](../../../internal/vcs/githubfake) serves GitHub App
and installation routes and none of the OAuth or user routes.

The reference's `oauthRedirect`, `oauthToken` and `oauthUser` are
`POST /auth/github/redirect`, `/auth/github/token` and `/auth/github/user`,
each with no security requirement, in
[`api/reference/spacelift/spacelift.openapi.yaml`](../../../api/reference/spacelift/spacelift.openapi.yaml).

## 8. OIDC test identity providers

| Option | Headless code flow with PKCE | Chosen `sub`, e-mail, groups | Issuers | Notes |
|---|---|---|---|---|
| navikt mock-oauth2-server 7.0.1 (MIT) | The login form takes a user name and a JSON of claims, so a test posts them; PKCE is checked; the nonce and `auth_time` are in the ID token | `sub` from the user name; any claim from the form | Any number in one container: the first path segment is the issuer | JVM; an end-session endpoint |
| Dex v2.46.1 (Apache-2.0) | Plain HTML forms with `skipApprovalScreen` | Static users take groups, `emailVerified` and names; `sub` is an encoding of the user and connector IDs and cannot be chosen | One per container | `prompt`, `max_age`, RP-initiated and back-channel logout only with `DEX_SESSIONS_ENABLED=true` |
| Keycloak 26.8.2 | Needs the login form's action URL and cookies scraped | Through user attributes and mappers | One realm per issuer | Heavy; the most complete logout |
| zitadel `op` in the test process | Yes | Through a 27-method storage interface | Any | Encrypts codes with A256GCM and falls back to CFB, so `fips140=only` fails |
| axa-group oauth2-mock-server | Issues a code at once | Only through JavaScript hooks | One per instance | No published image; validates no request |
| The repository's in-process chi issuer | With `/authorize` and `/token` added | Fully | One per instance | Today serves discovery, JWKS and minted tokens with no `x5c`; it alone can inject faults such as `x5c`, key rotation, an unknown `kid`, a wrong nonce or issuer, or a missing `auth_time` |

[S31][S32][S33][S34][S35]

The in-process issuer is
[`test/deps/oidc.go`](../../../test/deps/oidc.go). Being written from the same
reading of the specification as the relying party, it cannot find an
interoperability fault on its own.

## Sources

- [S1] [Go 1.27.4 FIPS 140-3 mode](https://go.dev/doc/security/fips140), [crypto/fips140](https://pkg.go.dev/crypto/fips140) and [crypto/rsa FIPS checks](https://tip.golang.org/src/crypto/rsa/fips.go).
- [S2] [go-jose v4.1.9 `jwk.go`](https://github.com/go-jose/go-jose/blob/v4.1.9/jwk.go).
- [S3] [go-jose pull request 219, conditional thumbprint check](https://github.com/go-jose/go-jose/pull/219) and [issue 187, GCM under FIPS](https://github.com/go-jose/go-jose/issues/187).
- [S4] Discovery documents and JWKS read 2026-11-24: [Entra ID](https://login.microsoftonline.com/common/v2.0/.well-known/openid-configuration), [Google](https://accounts.google.com/.well-known/openid-configuration), [GitHub Actions](https://token.actions.githubusercontent.com/.well-known/openid-configuration) and an Auth0 tenant's `/.well-known/openid-configuration`.
- [S5] [go-oidc v3 API](https://pkg.go.dev/github.com/coreos/go-oidc/v3/oidc), [v3.21.0 `go.mod`](https://github.com/coreos/go-oidc/blob/v3.21.0/go.mod), [v3.21.0 `jwks.go`](https://github.com/coreos/go-oidc/blob/v3.21.0/oidc/jwks.go) and [releases](https://github.com/coreos/go-oidc/releases).
- [S6] [go-oidc back-channel logout, pull request 486](https://github.com/coreos/go-oidc/pull/486).
- [S7] [`golang.org/x/oauth2` PKCE API](https://pkg.go.dev/golang.org/x/oauth2).
- [S8] [zitadel/oidc](https://github.com/zitadel/oidc), [`rp` API](https://pkg.go.dev/github.com/zitadel/oidc/v3/pkg/client/rp) and [v3.53.0 `go.mod`](https://github.com/zitadel/oidc/blob/v3.53.0/go.mod).
- [S9] [OpenID Connect Core 1.0](https://openid.net/specs/openid-connect-core-1_0.html) and [RP-Initiated Logout 1.0](https://openid.net/specs/openid-connect-rpinitiated-1_0.html).
- [S10] [OpenID Connect Back-Channel Logout 1.0](https://openid.net/specs/openid-connect-backchannel-1_0.html).
- [S11] [Google OpenID Connect](https://developers.google.com/identity/openid-connect/openid-connect).
- [S12] [Entra ID optional claims reference](https://learn.microsoft.com/en-us/entra/identity-platform/optional-claims-reference).
- [S13] [Okta groups claim](https://developer.okta.com/docs/guides/customize-tokens-groups-claim/main/).
- [S14] [Entra ID group claims and app roles](https://learn.microsoft.com/en-us/security/zero-trust/develop/configure-tokens-group-claims-app-roles).
- [S15] [Auth0 custom claims](https://auth0.com/docs/secure/tokens/json-web-tokens/create-custom-claims).
- [S16] [crewjam/saml](https://github.com/crewjam/saml), [`x/samlsp`](https://pkg.go.dev/github.com/crewjam/saml/x/samlsp), [`x/samlidp`](https://pkg.go.dev/github.com/crewjam/saml/x/samlidp), [`go.mod`](https://github.com/crewjam/saml/blob/main/go.mod) and [`xmlenc/gcm.go`](https://github.com/crewjam/saml/blob/main/xmlenc/gcm.go).
- [S17] [crewjam/saml advisories](https://github.com/crewjam/saml/security/advisories) and [GHSA-ppgc-j585-q4j4](https://github.com/crewjam/saml/security/advisories/GHSA-ppgc-j585-q4j4).
- [S18] [goxmldsig versions](https://pkg.go.dev/github.com/russellhaering/goxmldsig?tab=versions) and [advisories](https://github.com/russellhaering/goxmldsig/security/advisories).
- [S19] [gosaml2](https://github.com/russellhaering/gosaml2) and [advisories](https://github.com/russellhaering/gosaml2/security/advisories).
- [S20] [kristophjunge/test-saml-idp](https://hub.docker.com/r/kristophjunge/test-saml-idp).
- [S21] [BoxyHQ mock-saml](https://github.com/boxyhq/mock-saml).
- [S22] [elimity-com/scim](https://github.com/elimity-com/scim), [API](https://pkg.go.dev/github.com/elimity-com/scim) and [commits](https://github.com/elimity-com/scim/commits/master).
- [S23] [scim2/filter-parser v2](https://pkg.go.dev/github.com/scim2/filter-parser/v2?tab=versions).
- [S24] [Microsoft Entra SCIM validator](https://learn.microsoft.com/en-us/entra/identity/app-provisioning/scim-validator-tutorial).
- [S25] [scim2-tester](https://github.com/python-scim/scim2-tester).
- [S26] [go-webauthn versions](https://pkg.go.dev/github.com/go-webauthn/webauthn?tab=versions), [`webauthn` API](https://pkg.go.dev/github.com/go-webauthn/webauthn/webauthn), [`protocol` API](https://pkg.go.dev/github.com/go-webauthn/webauthn/protocol) and [`attestation.go`](https://github.com/go-webauthn/webauthn/blob/master/protocol/attestation.go).
- [S27] [descope/virtualwebauthn](https://github.com/descope/virtualwebauthn).
- [S28] [Chrome DevTools protocol WebAuthn domain](https://chromedevtools.github.io/devtools-protocol/tot/WebAuthn/).
- [S29] [`golang.org/x/oauth2/github`](https://pkg.go.dev/golang.org/x/oauth2/github).
- [S30] [GitHub OAuth app authorization](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps).
- [S31] [navikt mock-oauth2-server](https://github.com/navikt/mock-oauth2-server).
- [S32] [Dex local connector](https://dexidp.io/docs/connectors/local/) and [Dex v2.46.1 release](https://github.com/dexidp/dex/releases/tag/v2.46.1).
- [S33] [Keycloak 26.8.2 release](https://github.com/keycloak/keycloak/releases/tag/26.8.2).
- [S34] [zitadel `op` `crypto.go` at v3.53.0](https://github.com/zitadel/oidc/blob/v3.53.0/pkg/op/crypto.go).
- [S35] [axa-group oauth2-mock-server](https://github.com/axa-group/oauth2-mock-server).
