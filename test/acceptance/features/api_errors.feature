@postgres
Feature: The route table decides every API operation
  Every operation names its action and where its subject's space comes from.
  Before an operation runs, the server finds the subject's space and decides
  the action there. A space or entity that the account lacks answers 404,
  and so does one whose space the caller cannot read, with one body for
  both, so a 404 does not say which. An action the caller may not perform
  answers 403 forbidden, and the problem names the action. The home key is
  space-admin on its account's top space; the member key is a key of the
  same account with no binding. A view lists the actions the rules of its
  operations let the caller perform, so an API key that holds every action
  on API keys lists only their read while the account lets only people
  manage keys.
  ref: decisions 0007 and 0022

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"

  @op:managedUserRequestRoleChange @case:denial
  Scenario: A space the caller cannot read answers as one the account lacks
    When token "m" sends "POST /api/v1/spaces/legacy/role-change-requests" with '{"requestedAccessLevel":"WRITE"}' through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/spaces/nowhere/role-change-requests" with '{"requestedAccessLevel":"WRITE"}' through B
    Then it answers a 404 problem "not-found"
    And the answers for "legacy" and "nowhere" are one problem but for the ID

  @op:apiKeyCreate @op:sessions @op:sessionDeleteAll @case:denial
  Scenario: An action the caller may not perform answers 403 naming it
    When token "m" sends "POST /api/v1/api-keys" with '{"name":"deploy","teams":[]}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"API_KEY_CREATE"'
    When token "m" sends "GET /api/v1/sessions" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SESSION_READ"'
    When token "m" sends "DELETE /api/v1/sessions" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SESSION_DELETE"'

  @op:apiKeys @op:viewer @case:success
  Scenario: Each view lists the actions its operations let the caller perform
    When token "h" sends "GET /api/v1/api-keys" through A
    Then each item's allowedActions is '["API_KEY_READ"]'
    When token "h" sends "GET /api/v1/viewer" through B
    Then the answer's admin is 'true'
    When token "m" sends "GET /api/v1/api-keys" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"API_KEY_READ"'
    When token "m" sends "GET /api/v1/viewer" through B
    Then the answer's actions is '[]'
