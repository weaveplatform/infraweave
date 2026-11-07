@postgres
Feature: Personal API keys
  A personal API key is a member's own in one account (decision 0006). Its
  token acts as the owner in the key's account, with the owner's login and
  teams, so it never exceeds them. Only a member's own session creates one,
  and only while the account's personalApiKeysEnabled is on. Removing the
  owner from the account, or deactivating them, disables the key as
  DEPROVISIONED and ends its sessions, and its exchange answers 401 like any
  refused key's (capture C123). A personal key rides on its owner's seat.

  Background:
    Given two infraweave servers, A and B, on one database, with an operator key
    And the home key is exchanged through A for token "h"
    And the home account's member has a browser session, admitted to home alone
    And the home account's member is "alice"

  @op:personalApiKeyCreate @op:personalApiKeys @op:personalApiKeysCount @op:personalApiKeyDisable @op:managedUserPersonalApiKeys @case:success @case:failure
  Scenario: A member creates a personal key, which the account lists, counts and disables
    When the browser sends "POST /api/v1/personal-api-keys" with '{"name":"laptop","description":"mine"}' through A
    Then it answers 201
    And the answer's isPersonal is 'true'
    And the answer's owner is '"alice"'
    And the answer's key is the key "laptop"
    When token "h" sends "GET /api/v1/personal-api-keys" through B
    Then the answer lists the keys "laptop"
    When token "h" sends "GET /api/v1/managed-users/{alice}/personal-api-keys" through A
    Then the answer lists the keys "laptop"
    When token "h" sends "GET /api/v1/personal-api-keys-count" through B
    Then the answer is '1'
    When the browser sends "POST /api/v1/personal-api-keys/{laptop}/disable" through A
    Then the answer's disabledReason is '"ADMIN"'
    And exchanging the key "laptop" answers a 401 problem "unauthenticated"
    When token "h" sends "GET /api/v1/personal-api-keys-count" through B
    Then the answer is '0'
    When the browser sends "POST /api/v1/personal-api-keys/nope/disable" through A
    Then it answers a 404 problem "not-found"

  @op:personalApiKeys @op:personalApiKeysCount @op:personalApiKeyDisable @case:denial
  Scenario: Another account sees none of an account's personal keys, and only people disable them
    Given the sibling key is exchanged through A for token "s"
    When the browser sends "POST /api/v1/personal-api-keys" with '{"name":"laptop"}' through A
    Then the answer's key is the key "laptop"
    When token "s" sends "GET /api/v1/personal-api-keys" through B
    Then the answer lists the keys ""
    When token "s" sends "GET /api/v1/personal-api-keys-count" through A
    Then the answer is '0'
    When token "h" sends "POST /api/v1/personal-api-keys/{laptop}/disable" through B
    Then it answers a 403 problem "forbidden"
    When token "h" sends "GET /api/v1/personal-api-keys-count" through A
    Then the answer is '1'

  @op:personalApiKeyCreate @op:managedUserDelete @case:success @case:denial
  Scenario: A personal key's token acts as its owner in its account alone, until the owner is removed
    Given the sibling key is exchanged through A for token "s"
    When the browser sends "POST /api/v1/personal-api-keys" with '{"name":"laptop"}' through A
    Then the answer's key is the key "laptop"
    And the key "laptop" exchanges through B for token "p"
    When token "p" sends "GET /api/v1/viewer" through A
    Then the answer's name is '"Alice"'
    When token "p" sends "GET /api/v1/managed-users" through B
    Then the answer lists the managed users "alice"
    When token "s" sends "POST /api/v1/managed-users" with '{"username":"bob","roleBindings":[]}' through A
    And the answer's id names "bob"
    And token "p" sends "GET /api/v1/managed-users/{bob}" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "DELETE /api/v1/managed-users/{alice}" through A
    Then it answers 200
    And exchanging the key "laptop" answers a 401 problem "unauthenticated"
    And "GET /api/v1/viewer" through B with token "p" eventually answers 401
    When token "h" sends "GET /api/v1/api-keys/{laptop}" through A
    Then the answer's disabledReason is '"DEPROVISIONED"'

  @op:personalApiKeyCreate @case:denial @case:failure
  Scenario: Only a member creates a personal key, and only while the account allows it
    When token "h" sends "POST /api/v1/personal-api-keys" with '{"name":"ci"}' through A
    Then it answers a 403 problem "forbidden"
    When the browser sends "POST /api/v1/personal-api-keys" with '{"name":""}' through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "POST /api/v1/account/set-personal-api-keys-enabled" with '{"enabled":false}' through B
    And the browser sends "POST /api/v1/personal-api-keys" with '{"name":"laptop"}' through A
    Then it answers a 403 problem "forbidden"
