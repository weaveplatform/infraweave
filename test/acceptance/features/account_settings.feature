@postgres
Feature: Account settings
  An account reads and sets the settings no other domain owns: whether
  personal API keys are enabled, whether API keys may manage API keys, its
  security contact, its OAuth redirect allowlist, its awareness survey and
  its deletion mark. Each reads its default until it is set. API keys manage
  API keys only once a member allows it, and only a member may change that,
  so a key cannot grant itself. A request to enable personal keys is
  recorded on the account's trail; refusing personal keys at sign-in comes
  with personal keys themselves. An account is deleted only once marked,
  and never as its organization's last account; its deletion revokes its
  sessions on every replica and is recorded on its organization's trail.
  The other domains' purges then remove the rows they keep of it, such as
  its stacks and limits, and leave its organization's.

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "admin"

  @op:personalApiKeysEnabled @op:accountSetPersonalApiKeysEnabled @op:accountRequestPersonalApiKeysEnabling @case:success
  Scenario: Personal API keys are enabled until the account turns them off
    Then "GET /api/v1/personal-api-keys-enabled" through A with token "admin" answers 'true'
    When "POST /api/v1/account/set-personal-api-keys-enabled" through B with token "admin" and '{"enabled":false}' answers 'false'
    Then "GET /api/v1/personal-api-keys-enabled" through A with token "admin" answers 'false'
    When "POST /api/v1/account/request-personal-api-keys-enabling" through A with token "admin" answers 'true'
    Then the home account's audit trail holds "account.request_personal_api_keys_enabling"

  @op:apiKeysManageableByNonHumans @op:accountToggleAPIKeyManagementFromNonHumans @op:apiKeysManagedByNonAdmins @case:denial
  Scenario: API keys manage no API keys until a member allows it, which a key cannot
    Then "GET /api/v1/api-keys-manageable-by-non-humans" through A with token "admin" answers 'false'
    And "GET /api/v1/api-keys-managed-by-non-admins" through B with token "admin" answers 'false'
    And "POST /api/v1/account/toggle-api-key-management-from-non-humans" through A with token "admin" answers a 403 problem "forbidden"
    And creating a key through B with token "admin" and '{"name":"deploy","teams":[]}' answers a 403 problem "forbidden"
    And "GET /api/v1/api-keys-manageable-by-non-humans" through B with token "admin" answers 'false'

  @op:accountToggleAPIKeyManagementFromNonHumans @case:success
  Scenario: A member lets API keys manage API keys
    Given the home account's member has a browser session, admitted to home alone
    When "POST /api/v1/account/toggle-api-key-management-from-non-humans" through A in the browser answers 'true'
    Then "GET /api/v1/api-keys-manageable-by-non-humans" through B with token "admin" answers 'true'
    And token "admin" creates the secret key "deploy" with team "ops" through B

  @op:securityEmail @op:accountUpdateSecurityEmail @case:success @case:failure
  Scenario: The security contact is empty until set, and only an address is accepted
    Then "GET /api/v1/security-email" through A with token "admin" answers '""'
    When "POST /api/v1/account/update-security-email" through B with token "admin" and '{"securityEmail":"security@example.com"}' answers '"security@example.com"'
    Then "GET /api/v1/security-email" through A with token "admin" answers '"security@example.com"'
    And "POST /api/v1/account/update-security-email" through A with token "admin" and '{"securityEmail":"Security <security@example.com>"}' answers a 422 problem "validation-failed"
    And "GET /api/v1/security-email" through B with token "admin" answers '"security@example.com"'

  @op:oauthRedirectAllowedHosts @op:accountSetOAuthRedirectAllowlist @case:success @case:failure
  Scenario: The OAuth redirect allowlist holds lowercased hosts, and a URL is refused
    Then "GET /api/v1/oauth-redirect-allowed-hosts" through A with token "admin" answers '{"items":[]}'
    When "POST /api/v1/account/set-o-auth-redirect-allowlist" through B with token "admin" and '{"hosts":["Apps.Example.com","*.preview.example.com"]}' answers '["apps.example.com","*.preview.example.com"]'
    Then "GET /api/v1/oauth-redirect-allowed-hosts" through A with token "admin" answers '{"items":["apps.example.com","*.preview.example.com"]}'
    And "POST /api/v1/account/set-o-auth-redirect-allowlist" through A with token "admin" and '{"hosts":["ok.example.com","https://apps.example.com"]}' answers a 422 problem "validation-failed"
    And the problem's detail names "hosts[1]"
    And "GET /api/v1/oauth-redirect-allowed-hosts" through B with token "admin" answers '{"items":["apps.example.com","*.preview.example.com"]}'

  @op:awarenessSourceSurvey @op:accountUpdateAwarenessSourceSurvey @case:success
  Scenario: The awareness survey is incomplete until answered
    Then "GET /api/v1/awareness-source-survey" through A with token "admin" answers '{"completed":false}'
    When "POST /api/v1/account/update-awareness-source-survey" through B with token "admin" and '{"completed":true,"response":"a colleague"}' answers '{"completed":true,"response":"a colleague"}'
    Then "GET /api/v1/awareness-source-survey" through A with token "admin" answers '{"completed":true,"response":"a colleague"}'

  @op:markedForDeletion @op:accountToggleDeletionMark @op:accountCanBeDeleted @op:accountCanBeDeletedAt @op:accountConfirmDelete @case:success @case:failure
  Scenario: A marked account is deleted, and an unmarked one is not
    Given the sibling key is exchanged through A for token "sib"
    And the account "acme-dev" holds a stack and an account limit
    Then "GET /api/v1/marked-for-deletion" through A with token "sib" answers 'false'
    And "GET /api/v1/account/can-be-deleted" through B with token "sib" answers 'false'
    And "GET /api/v1/account/can-be-deleted-at" through A with token "sib" answers '0'
    And "POST /api/v1/account/confirm-delete" through B with token "sib" answers a 409 problem "invalid-state"
    When "POST /api/v1/account/toggle-deletion-mark" through A with token "sib" and '{"reason":"moving"}' answers 'true'
    And "POST /api/v1/account/toggle-deletion-mark" through B with token "sib" answers 'false'
    Then "GET /api/v1/marked-for-deletion" through A with token "sib" answers 'false'
    When "POST /api/v1/account/toggle-deletion-mark" through B with token "sib" and '{"reason":"moving","description":"to acme"}' answers 'true'
    Then "GET /api/v1/marked-for-deletion" through A with token "sib" answers 'true'
    And "GET /api/v1/account/can-be-deleted" through B with token "sib" answers 'true'
    And "GET /api/v1/account/can-be-deleted-at" through A with token "sib" answers a time within the last minute
    When "POST /api/v1/account/confirm-delete" through B with token "sib" answers 'true'
    Then the viewer through A with token "sib" answers 401 within a second
    And the sibling account no longer exists, and its organization's trail holds its deletion
    And no stack or limit of the account "acme-dev" remains, and its organization's entitlement does
    And the viewer through A with token "admin" answers 200

  @op:accountConfirmDelete @case:denial
  Scenario: An organization's last account is not deleted
    When "POST /api/v1/account/toggle-deletion-mark" through A with token "admin" answers 'true'
    Then "GET /api/v1/account/can-be-deleted" through B with token "admin" answers 'false'
    And "POST /api/v1/account/confirm-delete" through B with token "admin" answers a 409 problem "invalid-state"
    And the viewer through A with token "admin" answers 200
