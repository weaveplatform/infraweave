@postgres
Feature: Idempotency replay belongs to the authenticated account context
  A member keeps actor identity across account selection and credential renewal.
  Each admitted account owns its response and resource for the same request key.
  Initial identity cookies are fixture-seeded; selection, signed CLI issuance,
  role binding, policy writes and replay use actual public services.
  ref: decisions 0003, 0006, 0007 and 0013; #726

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the sibling key is exchanged through B for token "s"
    And the home account's member has a browser session, admitted to home and sibling
    And the home account's member is "alice"
    And token "s" sends "POST /api/v1/user-role-bindings" with '{"userID":"{alice}","roleID":"space-admin","spaceID":"acme-dev"}' through B
    And it answers 201

  @op:policyCreatev2 @case:success
  Scenario: The same signed member creates and replays one policy per account across selection and replicas
    Given the browser asks through A for a CLI token sealed to a new RSA key, as token "a1"
    When token "a1" creates its policy "policy-a" in the home account using key "same-account-switch-key" through A
    And the browser asks through B for a CLI token sealed to a new RSA key, as token "a2"
    Then token "a2" replays its policy "policy-a" in the home account using key "same-account-switch-key" through B
    When the browser switches to the sibling account through B
    And the browser asks through A for a CLI token sealed to a new RSA key, as token "b1"
    Then token "b1" creates its policy "policy-b" in the sibling account using key "same-account-switch-key" through B
    And token "b1" replays its policy "policy-b" in the sibling account using key "same-account-switch-key" through A
    When the browser switches to the home account through A
    And the browser asks through B for a CLI token sealed to a new RSA key, as token "a3"
    Then token "a3" replays its policy "policy-a" in the home account using key "same-account-switch-key" through B


  @op:policyCreatev2 @case:failure
  Scenario: Removing A's grant does not expose its stored response in authorized B
    Given the browser asks through A for a CLI token sealed to a new RSA key, as token "a1"
    And token "a1" creates its policy "policy-a" in the home account using key "revoked-account-key" through A
    When token "h" sends "GET /api/v1/managed-users/{alice}/user-role-bindings" through A
    And the answer's binding of the role "space-admin" names "admin"
    And token "h" sends "DELETE /api/v1/user-role-bindings/{admin}" through B
    Then it answers 200
    When token "a1" retries the account replay request using key "revoked-account-key" through B
    Then it answers a 404 problem "not-found"
    When the browser switches to the sibling account through B
    And the browser asks through A for a CLI token sealed to a new RSA key, as token "b1"
    Then token "b1" creates its policy "policy-b" in the sibling account using key "revoked-account-key" through B
    And token "b1" replays its policy "policy-b" in the sibling account using key "revoked-account-key" through A

  @op:policyCreatev2 @case:success
  Scenario: Independent actors own different responses and an API key keeps replay across credentials
    Given the browser asks through A for a CLI token sealed to a new RSA key, as token "a1"
    And token "a1" creates its policy "member-policy" in the home account using key "two-actor-key" through A
    When token "h" creates its policy "key-policy" in the home account using key "two-actor-key" through B
    And the home key is exchanged through B for token "h2"
    Then token "h2" replays its policy "key-policy" in the home account using key "two-actor-key" through A
    And token "a1" replays its policy "member-policy" in the home account using key "two-actor-key" through B

  @op:policyCreatev2 @case:success
  Scenario Outline: Account-qualified replay preserves the dated policy deprecation notice
    Given token "h" creates and replays a "<type>" policy with its dated notice through both replicas

    Examples:
      | type           |
      | TASK           |
      | INITIALIZATION |
