@postgres @oidc
Feature: API keys
  An account manages its API keys through the API. Create and reset answer
  the secret once; only its SHA-256 is stored. Reset, disable and delete
  revoke every session of the key, so its tokens stop on every replica. A
  key of another account answers 404. ApiKeyInput has no admin field, since
  a key is an administrator through a space-admin binding on the account's
  top space, so a body that sets it answers 400. An OIDC key exchanges a JWT
  of its issuer, which infraweave fetches over HTTPS only and from the
  allowed issuers only, so a key cannot make the server fetch an arbitrary
  URL. The home account's first key is seeded through SQL, as no credential
  exists before it; every other key here is created through the API. Keys
  manage keys here because the home account allows it, a setting only a
  member turns on, which account_settings.feature covers. The servers trust
  the shared OIDC issuer's certificate and allow it alone. Reading the
  keys needs API_KEY_READ, which space-admin on any space holds and
  space-writer on the top space does not, and the managers setting needs
  the account admin. The member key, 01KACME000000000000000000A, holds
  only the bindings a scenario gives it.
  ref: decisions 0006 and 0007, DV-013, DV-014

  Background:
    Given two infraweave servers, A and B, on one database, allowing the shared OIDC issuer
    And the home account lets API keys manage API keys
    And the home key is exchanged through A for token "admin"

  @op:apiKeyCreate @op:apiKeyUser @op:apiKey @op:apiKeys @case:success
  Scenario: A created key answers its secret once and exchanges for a token of its account
    When token "admin" creates the secret key "deploy" with team "ops" through A
    Then the key "deploy" was answered with a secret, which reading it through B does not answer
    And the key "deploy" exchanges through B for token "d1"
    And the viewer through A with token "d1" is the key "deploy" with team "ops"
    And the keys listed through B with token "admin" are the seeded key and "deploy", with no secret

  @op:apiKeyUpdate @case:success
  Scenario: Updating a key changes the fields the body sets
    When token "admin" creates the secret key "deploy" with team "ops" through A
    And token "admin" updates the key "deploy" through B with '{"name":"deployer","teams":["ops","qa"]}'
    Then reading the key "deploy" through A with token "admin" answers name "deployer", teams "ops,qa" and description "created by acceptance"

  @op:apiKeyReset @case:success
  Scenario: Resetting a key stops its old secret and its tokens on every replica
    When token "admin" creates the secret key "deploy" with team "ops" through A
    And the key "deploy" exchanges through A for token "d1"
    And token "admin" resets the key "deploy" through A
    Then the viewer through B with token "d1" answers 401 within a second
    And exchanging the key "deploy" with its first secret answers a 401 problem "unauthenticated"
    And the key "deploy" exchanges through B for token "d2"

  @op:apiKeyDelete @case:success
  Scenario: Deleting a key stops its tokens and hides it
    When token "admin" creates the secret key "deploy" with team "ops" through A
    And the key "deploy" exchanges through A for token "d1"
    And token "admin" deletes the key "deploy" through A, which answers it deleted
    Then the viewer through B with token "d1" answers 401 within a second
    And reading the key "deploy" through B with token "admin" answers a 404 problem "not-found"
    And exchanging the key "deploy" answers a 401 problem "unauthenticated"

  @op:apiKeyUser @case:denial
  Scenario: A disabled key does not exchange
    When token "admin" creates the secret key "deploy" with team "ops" through A
    And the key "deploy" is disabled
    Then exchanging the key "deploy" answers a 401 problem "unauthenticated"

  @op:apiKeyCreate @op:apiKeyUser @case:success
  Scenario: A key's token lives no longer than the key
    When token "admin" creates the secret key "short" with team "ops", expiring in an hour, through A
    Then the key "short" exchanges through A for a token valid until the key expires

  @op:apiKeyCreate @case:denial
  Scenario: ApiKeyInput has no admin field
    Then creating a key through A with token "admin" and '{"name":"root","teams":[],"admin":false}' answers a 400 problem "invalid-request"
    And creating a key through A with token "admin" and '{"name":"","teams":[]}' answers a 422 problem "validation-failed"
    And the keys listed through B with token "admin" are the seeded key alone

  @op:apiKey @op:apiKeyUpdate @op:apiKeyReset @op:apiKeyDelete @case:denial
  Scenario: A key of another account answers 404
    When token "admin" creates the secret key "deploy" with team "ops" through A
    And the stranger account lets API keys manage API keys
    And the sibling account lets API keys manage API keys
    And the stranger key is exchanged through A for token "str"
    And the sibling key is exchanged through A for token "sib"
    Then token "str" reading, updating, resetting and deleting the key "deploy" through B each answer a 404 problem "not-found"
    And token "sib" reading, updating, resetting and deleting the key "deploy" through A each answer a 404 problem "not-found"
    And the key "deploy" exchanges through B for token "d1"

  @op:apiKeyCreate @op:apiKeyUser @case:success
  Scenario: An OIDC key exchanges its issuer's token, its mapped groups as teams
    When token "admin" creates an OIDC key "gha" for the shared issuer, client "infraweave", subject "repo:acme/app:ref:refs/heads/main" and claim "roles" as groups, through A
    Then the key "gha" was answered without a secret
    And the key "gha" exchanges through B an issuer token with sub "repo:acme/app:ref:refs/heads/main", aud "infraweave" and roles "deployers" for token "g1"
    And the viewer through A with token "g1" is the key "gha" with teams "deployers"

  @op:apiKeyUser @case:denial
  Scenario: An OIDC key refuses a token that is not its issuer's for it
    When token "admin" creates an OIDC key "gha" for the shared issuer, client "infraweave", subject "repo:acme/app:ref:refs/heads/main" and claim "roles" as groups, through A
    Then exchanging the key "gha" with an issuer token whose sub is "x-repo:acme/app:ref:refs/heads/main-evil" answers a 401 problem "unauthenticated"
    And exchanging the key "gha" with an issuer token whose aud is "other" answers a 401 problem "unauthenticated"
    And exchanging the key "gha" with an issuer token whose iss is "https://evil.example.com" answers a 401 problem "unauthenticated"
    And exchanging the key "gha" with an issuer token that expired an hour ago answers a 401 problem "unauthenticated"

  @op:apiKeyCreate @case:denial
  Scenario: An OIDC key's issuer must be HTTPS and allowed
    Then creating an OIDC key through A with token "admin" for the shared issuer over plain HTTP answers a 422 problem "validation-failed"
    And creating an OIDC key through A with token "admin" for "https://token.actions.githubusercontent.com" answers a 422 problem "validation-failed"

  @op:apiKeyEnableNonAdminManagers @op:apiKeysManagedByNonAdmins @case:success
  Scenario: The account records whether non-admins manage API keys
    Then setting non-admin API key managers through A with token "admin" to true answers true
    And "GET /api/v1/api-keys-managed-by-non-admins" through B with token "admin" answers 'true'
    And setting non-admin API key managers through B with token "admin" to false answers false
    And "GET /api/v1/api-keys-managed-by-non-admins" through A with token "admin" answers 'false'

  @op:apiKeys @op:apiKey @op:apiKeyEnableNonAdminManagers @case:denial
  Scenario: A key that writes in the top space but administers no space is refused the account's keys, which an admin of a space below the top reads
    Given the member key is exchanged through A for token "m"
    And token "admin" sends "POST /api/v1/api-key-role-bindings" with '{"apiKeyID":"01KACME000000000000000000A","roleID":"space-writer","spaceID":"acme"}' through A
    And the answer's id names "writer"
    When token "m" sends "GET /api/v1/api-keys" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"API_KEY_READ"'
    When token "m" sends "GET /api/v1/api-keys/01KACME0000000000000000003" through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PUT /api/v1/account/api-key-non-admin-managers" with '{"enable":true}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"ACCOUNT_ADMIN"'
    When token "admin" sends "DELETE /api/v1/api-key-role-bindings/{writer}" through A
    And token "admin" sends "POST /api/v1/api-key-role-bindings" with '{"apiKeyID":"01KACME000000000000000000A","roleID":"space-admin","spaceID":"legacy"}' through A
    And token "m" sends "GET /api/v1/api-keys" through A
    Then it answers 200
    When token "m" sends "GET /api/v1/api-keys/01KACME0000000000000000003" through A
    Then it answers 200
    When token "m" sends "PUT /api/v1/account/api-key-non-admin-managers" with '{"enable":true}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"ACCOUNT_ADMIN"'
