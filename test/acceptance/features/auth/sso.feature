@identity @oidc @postgres
Feature: Sign-in through an organization's OIDC provider
  A browser signs in to an organization through one of its enabled OIDC
  providers with the authorization code flow, PKCE and a nonce. The start
  stores the request, bound to the browser by the __Host-infraweave_sso
  cookie, and the one callback consumes it once, within 10 minutes, only
  from that browser. infraweave verifies the ID token with go-oidc over
  its own key cache, whose JWKS may carry x5c, as the in-process issuer's
  always does, and refuses a wrong nonce, issuer, audience or algorithm, an
  empty sub, several audiences without azp, a key the JWKS lacks, and for a
  re-authentication a missing or old auth_time. A refusal is a page naming
  its code and creates no session. The identity resolves to a member: an
  unknown subject creates an active member, a pending member invited under
  the login becomes active, a deactivated one is refused. The sign-in is a
  browser session of actor kind user in no account whose original teams are
  the groups; a second sign-in replaces the first. The provider's admin
  login holds organization-admin without a row, and an organization admin's
  enabling sign-in enables a provider. Logout answers the provider's
  end-session URL; a back-channel logout ends the provider's sessions on
  every replica, once per token. Organization admins in person, a member's
  session or CLI token whose member holds organization-admin or the
  break-glass admin, configure the default OIDC provider through the SSO
  tag: saving it reads its discovery document and stores it disabled, the
  saving admin's enabling sign-in turns single sign-on on, a new claim
  mapping applies at the next sign-in, and deleting it unlinks single
  sign-on, ending its sessions on every replica. Each scenario's issuers
  are its own: the in-process issuer, whose faults a step chooses, and an
  issuer in navikt's mock-oauth2-server.
  ref: decision 0006 § "Human login"

  Background:
    Given two infraweave servers, A and B, on one database, trusting the scenario's OIDC issuers
    And the home organization has the OIDC provider "okta" at the in-process issuer, enabled with the admin login "root"

  Scenario: A browser signs in through the in-process issuer on one replica and is signed in on both
    Given the provider "okta" signs in "nora" next with the claims '{"preferred_username":"nora","email":"nora@example.com","email_verified":true,"groups":["ops","dev"]}'
    When the browser starts signing in through the provider "okta" at A with "return_to=%2Fstacks"
    And the provider sends the browser back to B
    Then the sign-in sends the browser to "/stacks"
    And the browser's session is "nora"'s, signed in through "okta" with the teams "ops,dev"
    And the member "nora" is active with 1 identity
    And the viewer through A in the browser answers 200

  Scenario: A browser signs in through navikt's issuer
    Given the home organization has the OIDC provider "nav" at the navikt issuer, enabled with the admin login "root"
    And the provider "nav" signs in "nora-sub" next with the claims '{"email":"nora@example.com","email_verified":true,"groups":["ops"]}'
    When the browser signs in through the provider "nav" at A
    Then the browser's session is "nora@example.com"'s, signed in through "nav" with the teams "ops"

  Scenario: The organization's pages send the browser to its one provider
    Then "GET /auth/login" through A in the browser sends it to "/auth/login/acme"
    And "GET /auth/login/acme?return_to=%2Fstacks" through B in the browser sends it to "/auth/login/acme/{okta}?return_to=%2Fstacks"

  Scenario: The subject comes from the claim the provider maps
    Given the provider "okta" takes the subject from the claim "oid"
    And the provider "okta" signs in "pairwise-1" next with the claims '{"oid":"object-1","preferred_username":"olga"}'
    When the browser signs in through the provider "okta" at A
    And the provider "okta" signs in "pairwise-2" next with the claims '{"oid":"object-1","preferred_username":"olga"}'
    And the browser signs in through the provider "okta" at B
    Then the member "olga" is active with 1 identity
    And the provider "okta" signs in "pairwise-3" next with the claims '{"preferred_username":"olga"}'
    When the browser starts signing in through the provider "okta" at A
    And the provider sends the browser back to A
    Then the sign-in answers 401 with the code "unauthenticated"

  Scenario Outline: An ID token with the fault "<fault>" is refused and signs no one in
    Given the provider "okta" signs in "mallory" next with the fault "<fault>"
    When the browser starts signing in through the provider "okta" at A
    And the provider sends the browser back to A
    Then the sign-in answers 401 with the code "unauthenticated"
    And no session was signed in through "okta"

    Examples:
      | fault                 |
      | wrong-nonce           |
      | wrong-issuer          |
      | wrong-audience        |
      | hs256                 |
      | alg-none              |
      | unknown-kid           |
      | empty-sub             |
      | audiences-without-azp |

  Scenario Outline: A re-authentication refuses the fault "<fault>"
    Given the provider "okta" signs in "nora" next with the claims '{}'
    And the browser signs in through the provider "okta" at A
    And the provider "okta" signs in "nora" next with the fault "<fault>"
    When the browser starts signing in through the provider "okta" at B with "purpose=reauthenticate"
    And the provider sends the browser back to B
    Then the sign-in answers 401 with the code "unauthenticated"

    Examples:
      | fault         |
      | no-auth-time  |
      | old-auth-time |

  Scenario: A provider whose token endpoint is down answers a page, and a groups overage means no groups
    Given the provider "okta" signs in "nora" next with the fault "token-unavailable"
    When the browser starts signing in through the provider "okta" at A
    And the provider sends the browser back to A
    Then the sign-in answers 502 with the code "identity-provider-failed"
    And no session was signed in through "okta"
    Given the provider "okta" signs in "nora" next with the fault "groups-overage"
    When the browser signs in through the provider "okta" at A
    Then the browser's session is "nora"'s, signed in through "okta" with the teams ""

  Scenario: A rotated key is fetched at most once a minute for an unknown kid
    Given the provider "okta" signs in "nora" next with the claims '{}'
    And the browser signs in through the provider "okta" at A
    And the provider "okta"'s JWKS was read 1 time
    When the provider "okta"'s issuer rotates its key
    And the provider "okta" signs in "nora" next with the claims '{}'
    And the browser starts signing in through the provider "okta" at A
    And the provider sends the browser back to A
    Then the sign-in answers 401 with the code "unauthenticated"
    And the provider "okta"'s JWKS was read 1 time
    Given the provider "okta" signs in "nora" next with the claims '{}'
    When the browser signs in through the provider "okta" at B
    Then the provider "okta"'s JWKS was read 2 times

  Scenario: A callback is consumed once, only by its browser, and only within 10 minutes
    Given the provider "okta" signs in "nora" next with the claims '{}'
    When the browser starts signing in through the provider "okta" at A
    And another browser brings the callback to A
    Then the sign-in answers 400 with the code "invalid-request"
    And the browser brings the callback to B again
    And the sign-in sends the browser to "/"
    When the browser brings the callback to A again
    Then the sign-in answers 400 with the code "invalid-request"
    Given the provider "okta" signs in "nora" next with the claims '{}'
    When the browser starts signing in through the provider "okta" at A
    And the sign-in waits past its 10 minutes
    And the provider sends the browser back to A
    Then the sign-in answers 400 with the code "invalid-request"

  Scenario: A pending member becomes active, enters the account on the other replica, and a second sign-in replaces the first
    Given "carol" is a pending member of the home account
    And the provider "okta" signs in "carol-sub" next with the claims '{"preferred_username":"carol"}'
    When the browser signs in through the provider "okta" at A
    Then the member "carol" is active with 1 identity
    When the browser switches to the home account through B
    Then the account read through A in the browser is the home account
    Given the provider "okta" signs in "carol-sub" next with the claims '{"preferred_username":"carol"}'
    When the browser signs in through the provider "okta" at B
    Then the browser's ended cookie answers 401 at the viewer through A within a second
    And the member "carol" is active with 1 identity

  Scenario: A deactivated member is refused and signs no one in
    Given the provider "okta" signs in "dave-sub" next with the claims '{"preferred_username":"dave"}'
    And the browser signs in through the provider "okta" at A
    And the member "dave" is deactivated
    And the provider "okta" signs in "dave-sub" next with the claims '{"preferred_username":"dave"}'
    When the browser starts signing in through the provider "okta" at B
    And the provider sends the browser back to B
    Then the sign-in answers 403 with the code "member-deactivated"
    And the member "dave" is deactivated with 1 identity

  Scenario: The provider's admin login holds organization-admin, and an organization admin enables a provider
    Given the provider "okta" signs in "root-sub" next with the claims '{"preferred_username":"root"}'
    When the browser signs in through the provider "okta" at A
    And the browser sends "GET /api/v1/organization/identity-providers" through B
    Then it answers 200
    Given the home organization has the OIDC provider "entra" at the in-process issuer, disabled
    When the browser starts signing in through the provider "entra" at A
    Then the sign-in answers 404 with the code "not-found"
    Given the provider "entra" signs in "ada-sub" next with the claims '{"preferred_username":"ada"}'
    When the browser starts signing in through the provider "entra" at B with "purpose=enable"
    And the provider sends the browser back to A
    Then the sign-in sends the browser to "/"
    And the provider "entra" is enabled with the admin login "ada"
    And the home organization's trail holds 1 "identity_provider.enable" entry
    When the browser sends "GET /api/v1/organization/identity-providers" through B
    Then it answers 200
    Given the provider "okta" signs in "nora" next with the claims '{"preferred_username":"nora"}'
    When the browser signs in through the provider "okta" at A
    And the browser sends "GET /api/v1/organization/identity-providers" through B
    Then it answers a 403 problem "forbidden"
    When the browser starts signing in through the provider "okta" at A with "purpose=enable"
    Then the sign-in answers 403 with the code "forbidden"

  Scenario: A tier without oidc-sso refuses a sign-in's start
    Given the operator key is exchanged through A for token "op"
    When token "op" creates the tier "Gold" with the feature "organization" through A
    And token "op" gives the home organization the tier "Gold" through A
    And token "op" turns billing on through A
    Then starting a sign-in through the provider "okta" at B eventually answers 403 with the code "feature-not-in-tier"

  @op:logout @case:success
  Scenario: Logout answers the provider's end-session URL, and a back-channel logout ends the sessions on every replica once
    Given the provider "okta" signs in "nora" next with the claims '{}'
    And the browser signs in through the provider "okta" at A
    When the browser logs out through B and is sent on to the provider "okta"
    Then the browser's ended cookie answers 401 at the viewer through A within a second
    Given the provider "okta" signs in "nora" next with the claims '{}'
    And the browser signs in through the provider "okta" at A
    When the provider "okta" logs out "nora"'s session through B
    Then the browser's session is refused through A within a second
    And replaying the logout token through A answers 400
    Given the provider "okta" signs in "nora" next with the claims '{}'
    And the browser signs in through the provider "okta" at B
    When the provider "okta" logs out "nora"'s subject through A
    Then the browser's session is refused through B within a second

  @op:oidcSettings @op:oidcCreate @op:oidcUpdate @op:oidcDelete @op:hasSSO @case:success
  Scenario: An organization admin replaces the OIDC settings, enables them with a first sign-in, maps the groups claim and unlinks single sign-on
    Given the home account's member has a browser session, admitted to home alone
    And the home account's member holds organization-admin
    And the browser asks through A for a CLI token sealed to a new RSA key, as token "adm"
    When token "adm" sends "GET /api/v1/has-sso" through B
    Then the answer is 'true'
    When token "adm" sends "GET /api/v1/oidc/settings" through A
    Then the answer's adminLogin is '"root"'
    And the answer's clientId is '"infraweave-okta"'
    When token "adm" sends "DELETE /api/v1/oidc" through B
    Then the answer's adminLogin is '"root"'
    When token "adm" creates the OIDC settings "nav" at the navikt issuer through A
    Then it answers 201
    And the answer's adminLogin is 'null'
    And the answer's claimMapping is '{"entries":[]}'
    When token "adm" sends "GET /api/v1/oidc/settings" through B
    Then the answer's adminLogin is 'null'
    And the answer's clientId is '"infraweave-nav"'
    When token "adm" sends "GET /api/v1/has-sso" through A
    Then the answer is 'false'
    When the browser starts signing in through the provider "nav" at A
    Then the sign-in answers 404 with the code "not-found"
    Given the provider "nav" signs in "ada-sub" next with the claims '{"preferred_username":"ada"}'
    When the browser starts signing in through the provider "nav" at B with "purpose=enable"
    And the provider sends the browser back to A
    Then the sign-in sends the browser to "/"
    And the home organization's trail holds 1 "identity_provider.enable" entry
    When token "adm" sends "GET /api/v1/has-sso" through B
    Then the answer is 'true'
    When token "adm" sends "GET /api/v1/oidc/settings" through A
    Then the answer's adminLogin is '"ada"'
    When token "adm" sends "PUT /api/v1/oidc" with '{"claimMapping":{"entries":[{"name":"groups","value":"roles"}]}}' through B
    Then the answer's claimMapping is '{"entries":[{"name":"groups","value":"roles"}]}'
    Given the provider "nav" signs in "nora-sub" next with the claims '{"preferred_username":"nora","roles":["ops"],"groups":["dev"]}'
    When the browser signs in through the provider "nav" at A
    Then the browser's session is "nora"'s, signed in through "nav" with the teams "ops"
    And the member "nora" is active with 1 identity
    When token "adm" sends "DELETE /api/v1/oidc" through A
    Then the answer's adminLogin is '"ada"'
    And the browser's session is refused through B within a second
    And no live session is signed in through "nav"
    And the member "nora" is active with 0 identities
    When token "adm" sends "GET /api/v1/has-sso" through B
    Then the answer is 'false'

  @op:oidcSettings @op:oidcUpdate @op:hasSSO @case:success
  Scenario: The break-glass admin reads and changes the OIDC settings
    Given the password "correct horse" hashed by infraweave auth hash-password
    And the operator key is exchanged through A for token "op"
    And token "op" sets the break-glass login of "acme" to "admin" through A
    When the browser signs in to "acme" as "admin" with the password "correct horse" through B
    And the browser sends "GET /api/v1/oidc/settings" through A
    Then the answer's adminLogin is '"root"'
    When the browser sends "PUT /api/v1/oidc" with '{"claimMapping":{"entries":[{"name":"subject","value":"oid"}]}}' through B
    Then the answer's claimMapping is '{"entries":[{"name":"subject","value":"oid"}]}'
    When the browser sends "GET /api/v1/has-sso" through A
    Then the answer is 'true'

  @op:oidcSettings @op:oidcCreate @op:oidcUpdate @op:oidcDelete @op:hasSSO @case:denial
  Scenario: An account admin without the organization role is refused every single sign-on operation, and a tier without oidc-sso refuses the OIDC settings
    Given the home account's member has a browser session, admitted to home alone
    When the browser sends "GET /api/v1/oidc/settings" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"ORGANIZATION_ADMIN"'
    When the browser sends "GET /api/v1/has-sso" through B
    Then it answers a 403 problem "forbidden"
    When the browser sends "POST /api/v1/oidc" with '{"identityProviderHost":"https://acme.okta.com","clientId":"a","clientCredentials":"s"}' through A
    Then it answers a 403 problem "forbidden"
    When the browser sends "PUT /api/v1/oidc" with '{}' through B
    Then it answers a 403 problem "forbidden"
    When the browser sends "DELETE /api/v1/oidc" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"ORGANIZATION_ADMIN"'
    And the home organization's trail holds 0 "identity_provider.delete" entries
    Given the home account's member holds organization-admin
    And the operator key is exchanged through A for token "op"
    When token "op" creates the tier "Gold" with the feature "account-settings" through A
    And token "op" gives the home organization the tier "Gold" through A
    And token "op" turns billing on through A
    Then the browser's "GET /api/v1/oidc/settings" with '' through B eventually answers a 403 problem "feature-not-in-tier"
    And the problem names the feature "oidc-sso"
    When the browser sends "GET /api/v1/has-sso" through A
    Then the answer is 'true'

  @op:oidcSettings @op:oidcCreate @op:oidcUpdate @op:oidcDelete @op:hasSSO @case:failure
  Scenario: A second OIDC provider, a bad host, an invalid claim mapping and missing settings are refused, and an organization token is no credential
    Given the home account's member has a browser session, admitted to home alone
    And the home account's member holds organization-admin
    When the browser sends "POST /api/v1/oidc" with '{"identityProviderHost":"{okta issuer}","clientId":"other","clientCredentials":"s"}' through A
    Then it answers a 409 problem "already-exists"
    When the browser sends "POST /api/v1/oidc" with '{"identityProviderHost":"http://acme.okta.com","clientId":"a","clientCredentials":"s"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "identityProviderHost"
    When the browser sends "POST /api/v1/oidc" with '{"identityProviderHost":"https://acme.okta.com","clientId":"a","clientCredentials":"s","claimMapping":{"entries":[{"name":"teams","value":"groups"}]}}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "claimMapping.entries[0].name"
    When the browser sends "POST /api/v1/oidc" with '{"identityProviderHost":"https://acme.okta.com","clientId":"a","clientCredentials":"s","colour":"red"}' through B
    Then it answers a 400 problem "invalid-request"
    And the problem refuses the field "/colour" as "unknown-field"
    When the browser sends "PUT /api/v1/oidc" with '{"claimMapping":{"entries":[{"name":"groups","value":""}]}}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "claimMapping.entries[0].value"
    When the browser sends "DELETE /api/v1/oidc" through B
    Then it answers 200
    When the browser sends "POST /api/v1/oidc" with '{"identityProviderHost":"{okta issuer}/","clientId":"infraweave-okta","clientCredentials":"s"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "identityProviderHost"
    When the browser sends "POST /api/v1/oidc" with '{"identityProviderHost":"https://127.0.0.1:9","clientId":"a","clientCredentials":"s"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "identityProviderHost"
    When the browser sends "GET /api/v1/oidc/settings" through A
    Then it answers a 404 problem "not-found"
    When the browser sends "PUT /api/v1/oidc" with '{}' through B
    Then it answers a 404 problem "not-found"
    When the browser sends "DELETE /api/v1/oidc" through A
    Then it answers a 404 problem "not-found"
    And the home organization's trail holds 0 "identity_provider.create" entries
    When the browser sends "GET /api/v1/has-sso" through B
    Then the answer is 'false'
    When the browser creates the organization key "k" through A
    And the organization key "k" exchanges through B for token "org"
    And token "org" sends "GET /api/v1/has-sso" through A
    Then it answers a 401 problem "unauthenticated"
    When token "org" sends "GET /api/v1/oidc/settings" through B
    Then it answers a 401 problem "unauthenticated"
