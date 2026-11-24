@identity @oidc
Feature: Test OIDC issuers for sign-in
  Sign-in scenarios reach two OpenID Providers of their own: navikt's
  mock-oauth2-server in a container, a third-party implementation that
  proves interoperability, and the in-process issuer, whose faults a
  scenario chooses. Each runs the authorization code flow with PKCE and a
  nonce, and its ID tokens verify against its JWKS.

  Scenario: A browser signs in at the navikt issuer through its form
    Given an OIDC issuer of the scenario's own in the navikt container
    And the issuer signs in "alice" next with the claims '{"email":"alice@example.com","email_verified":true,"groups":["ops"]}'
    When a relying party signs in through the issuer with PKCE
    Then the ID token verifies against the issuer's JWKS
    And the ID token's "sub" is '"alice"'
    And the ID token's "email" is '"alice@example.com"'
    And the ID token's "groups" is '["ops"]'
    And the ID token's nonce is the request's

  Scenario: A browser signs in at the in-process issuer as the identity it queued
    Given an in-process OIDC issuer of the scenario's own
    And the issuer signs in "bob" next with the claims '{"email":"bob@example.com","roles":["admins"]}'
    When a relying party signs in through the issuer with PKCE
    Then the ID token verifies against the issuer's JWKS
    And the ID token's "sub" is '"bob"'
    And the ID token's "roles" is '["admins"]'
    And the ID token's "email_verified" is 'null'
    And the ID token's nonce is the request's

  Scenario: The in-process issuer exchanges a code once
    Given an in-process OIDC issuer of the scenario's own
    And the issuer signs in "alice" next with the claims '{}'
    When a relying party signs in through the issuer with PKCE
    And the relying party exchanges the code again
    Then the token endpoint answers 400 with the error "invalid_grant"

  Scenario Outline: The in-process issuer answers the fault "<fault>"
    Given an in-process OIDC issuer of the scenario's own
    And the issuer signs in "alice" next with the fault "<fault>"
    When a relying party signs in through the issuer with PKCE
    Then the ID token verifies against the issuer's JWKS
    And the ID token's "<claim>" is '<value>'

    Examples:
      | fault          | claim     | value            |
      | empty-sub      | sub       | ""               |
      | wrong-audience | aud       | "another-client" |
      | no-auth-time   | auth_time | null             |
      | groups-overage | groups    | null             |

  Scenario Outline: The in-process issuer signs with "<alg>" for the fault "<fault>"
    Given an in-process OIDC issuer of the scenario's own
    And the issuer signs in "alice" next with the fault "<fault>"
    When a relying party signs in through the issuer with PKCE
    Then the ID token does not verify against the issuer's JWKS
    And the ID token's header "alg" is "<alg>"

    Examples:
      | fault       | alg   |
      | hs256       | HS256 |
      | alg-none    | none  |
      | unknown-kid | RS256 |

  Scenario: The in-process issuer's token endpoint can be down
    Given an in-process OIDC issuer of the scenario's own
    And the issuer signs in "alice" next with the fault "token-unavailable"
    When a relying party signs in through the issuer with PKCE
    Then the token endpoint answers 503 with the error "temporarily_unavailable"
