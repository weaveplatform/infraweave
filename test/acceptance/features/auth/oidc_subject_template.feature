@identity @postgres
Feature: The subject template of the account's OIDC federation tokens
  An account's admins set the template every run renders into the sub
  claim of its OIDC federation token, and each answer is the template in
  effect. A null or empty template restores the default. A template holds
  at most 1000 characters of letters, digits, "-", "_", ":", "/", "|" and
  the known placeholders, because cloud trust policies match the subject
  as written. Setting it needs ACCOUNT_ADMIN. The member key,
  01KACME000000000000000000A, holds only the bindings a scenario gives it.

  Background:
    Given two infraweave servers, A and B, on one database
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
