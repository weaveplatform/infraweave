@identity @postgres
Feature: The subject template of the account's OIDC federation tokens
  An account's admins set the template every run renders into the sub
  claim of its OIDC federation token, and each answer is the template in
  effect. A null or empty template restores the default. A template holds
  at most 1000 characters of letters, digits, "-", "_", ":", "/", "|" and
  the known placeholders, because cloud trust policies match the subject
  as written. Setting it needs ACCOUNT_ADMIN. The SSO tag's
  oidcSubjectTemplate reads the template in effect, the stored one or the
  default, to the account's admins and the organization admins in it,
  where every other operation of the tag admits organization admins alone.
  It needs a credential of the account, so the break-glass admin's session,
  which is in no account, is no credential for it, and it needs
  oidc-federation in the organization's tier. The member key,
  01KACME000000000000000000A, holds only the bindings a scenario gives it.
  ref: decision 0006 § "Members and identity providers"

  Background:
    Given two infraweave servers, A and B, on one database, with an operator key
    And the home key is exchanged through A for token "admin"

  @op:accountSetOIDCSubjectTemplate @case:success
  Scenario: An account admin sets the subject template on one replica and restores the default on the other
    When "POST /api/v1/account/set-oidc-subject-template" through A with token "admin" and '{"template":"space:{spacePath}:{callerType}:{callerId}:{scope}"}' answers '"space:{spacePath}:{callerType}:{callerId}:{scope}"'
    And "POST /api/v1/account/set-oidc-subject-template" through B with token "admin" and '{"template":null}' answers '"space:{spaceId}:{callerType}:{callerId}:run_type:{runType}:scope:{scope}"'
    And "POST /api/v1/account/set-oidc-subject-template" through A with token "admin" and '{"template":"stack|{callerId}"}' answers '"stack|{callerId}"'
    Then "POST /api/v1/account/set-oidc-subject-template" through B with token "admin" and '{"template":""}' answers '"space:{spaceId}:{callerType}:{callerId}:run_type:{runType}:scope:{scope}"'

  @op:accountSetOIDCSubjectTemplate @case:failure
  Scenario: A template with an unknown placeholder, a forbidden character or an unclosed brace is refused, naming it
    When "POST /api/v1/account/set-oidc-subject-template" through A with token "admin" and '{"template":"space:{spaceName}"}' answers a 422 problem "validation-failed"
    Then the problem's detail names "{spaceName}"
    When "POST /api/v1/account/set-oidc-subject-template" through B with token "admin" and '{"template":"space:{spaceId}?x=1"}' answers a 422 problem "validation-failed"
    Then the problem's detail names "'?'"
    When "POST /api/v1/account/set-oidc-subject-template" through A with token "admin" and '{"template":"space:{spaceId"}' answers a 422 problem "validation-failed"
    Then the problem's detail names "not closed"
    And "POST /api/v1/account/set-oidc-subject-template" through B with token "admin" and '{"template":"space:{spaceId}","extra":1}' answers a 400 problem "invalid-request"

  @op:accountSetOIDCSubjectTemplate @case:denial
  Scenario: A key that writes in the top space but does not administer the account cannot set the template
    Given the member key is exchanged through A for token "m"
    And token "admin" sends "POST /api/v1/api-key-role-bindings" with '{"apiKeyID":"01KACME000000000000000000A","roleID":"space-writer","spaceID":"acme"}' through A
    When token "m" sends "POST /api/v1/account/set-oidc-subject-template" with '{"template":"mine"}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"ACCOUNT_ADMIN"'

  @op:oidcSubjectTemplate @case:success
  Scenario: An account admin reads the default and then the template it set on the other replica, and an organization admin in the account reads the same
    Given the home account's member has a browser session, admitted to home alone, with no role
    And the home account's member holds organization-admin
    When token "admin" sends "GET /api/v1/oidc/subject-template" through A
    Then the answer is '"space:{spaceId}:{callerType}:{callerId}:run_type:{runType}:scope:{scope}"'
    When "POST /api/v1/account/set-oidc-subject-template" through A with token "admin" and '{"template":"stack|{callerId}:{scope}"}' answers '"stack|{callerId}:{scope}"'
    And token "admin" sends "GET /api/v1/oidc/subject-template" through B
    Then the answer is '"stack|{callerId}:{scope}"'
    When the browser sends "GET /api/v1/oidc/subject-template" through B
    Then the answer is '"stack|{callerId}:{scope}"'

  @op:oidcSubjectTemplate @case:denial
  Scenario: A member who is neither an account admin nor an organization admin cannot read the template
    Given the home account's member has a browser session, admitted to home alone, with no role
    When the browser sends "GET /api/v1/oidc/subject-template" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"ACCOUNT_ADMIN"'
    Given the member key is exchanged through A for token "m"
    And token "admin" sends "POST /api/v1/api-key-role-bindings" with '{"apiKeyID":"01KACME000000000000000000A","roleID":"space-writer","spaceID":"acme"}' through A
    When token "m" sends "GET /api/v1/oidc/subject-template" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"ACCOUNT_ADMIN"'

  @op:oidcSubjectTemplate @case:failure
  Scenario: A break-glass session, which is in no account, is no credential, and a tier without oidc-federation refuses the read
    Given the password "correct horse" hashed by infraweave auth hash-password
    And the operator key is exchanged through A for token "op"
    And token "op" sets the break-glass login of "acme" to "admin" through A
    When the browser signs in to "acme" as "admin" with the password "correct horse" through B
    And the browser sends "GET /api/v1/oidc/subject-template" through A
    Then it answers a 401 problem "unauthenticated"
    When token "op" creates the tier "Gold" with the feature "account-settings" through A
    And token "op" gives the home organization the tier "Gold" through A
    And token "op" turns billing on through A
    Then "GET /api/v1/oidc/subject-template" through B with token "admin" eventually answers a 403 problem "feature-not-in-tier"
    And the problem names the feature "oidc-federation"
