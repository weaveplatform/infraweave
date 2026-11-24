@postgres
Feature: Feature previews
  An account reads its feature previews: every feature in preview that the
  installation has not switched off, in catalogue order, the account's
  opt-in with whether the caller may change it, the caller's own setting
  when the caller is a member, and whether the list is visible at all. The
  catalogue marks no feature in preview yet, so the list is empty and not
  visible, and both settings are still stored and read back. The account's
  opt-in is off until an account admin turns it on, and every replica reads
  the change once its notification arrives. A member's own setting is on
  until the member opts out; it changes only what the member's clients
  show, and a personal API key reads and sets its owner's. A principal that
  names no member, such as an API key that is not personal, reads no
  personal setting and may not set one. The home key and the home
  account's member are account admins; the member key,
  01KACME000000000000000000A, holds only the bindings a scenario gives it.
  ref: decision 0021

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "admin"

  @op:featurePreview @op:accountSetFeaturePreviewsEnabled @case:success
  Scenario: An account admin opts the account in and out, and every replica reads it
    Then "GET /api/v1/feature-preview" through A with token "admin" answers '{"features":[],"account":{"enabled":false,"canManage":true},"visible":false}'
    When "POST /api/v1/account/set-feature-previews-enabled" through B with token "admin" and '{"enabled":true}' answers '{"features":[],"account":{"enabled":true,"canManage":true},"visible":false}'
    Then "GET /api/v1/feature-preview" through A with token "admin" eventually answers '{"features":[],"account":{"enabled":true,"canManage":true},"visible":false}'
    And "GET /api/v1/feature-preview" through B with token "admin" eventually answers '{"features":[],"account":{"enabled":true,"canManage":true},"visible":false}'
    When "POST /api/v1/account/set-feature-previews-enabled" through A with token "admin" and '{"enabled":false}' answers '{"features":[],"account":{"enabled":false,"canManage":true},"visible":false}'
    Then "GET /api/v1/feature-preview" through B with token "admin" eventually answers '{"features":[],"account":{"enabled":false,"canManage":true},"visible":false}'
    And "GET /api/v1/feature-preview" through A with token "admin" eventually answers '{"features":[],"account":{"enabled":false,"canManage":true},"visible":false}'

  @op:featurePreview @op:featurePreviewSetPersonalEnabled @op:accountSetFeaturePreviewsEnabled @case:success
  Scenario: A member opts out and back in, through the browser and through a personal key, and the account's setting is unchanged
    Given the home account's member has a browser session, admitted to home alone
    And the home account's member is "alice"
    When the browser sends "POST /api/v1/personal-api-keys" with '{"name":"laptop"}' through A
    Then the answer's key is the key "laptop"
    And the key "laptop" exchanges through B for token "p"
    And "GET /api/v1/feature-preview" through A in the browser answers '{"features":[],"account":{"enabled":false,"canManage":true},"personal":{"enabled":true},"visible":false}'
    When the browser sends "PUT /api/v1/feature-preview/personal" with '{"enabled":false}' through B
    Then it answers 200
    And the answer's personal is '{"enabled":false}'
    And the answer's account is '{"enabled":false,"canManage":true}'
    And "GET /api/v1/feature-preview" through A with token "p" answers '{"features":[],"account":{"enabled":false,"canManage":true},"personal":{"enabled":false},"visible":false}'
    And "GET /api/v1/feature-preview" through B with token "admin" answers '{"features":[],"account":{"enabled":false,"canManage":true},"visible":false}'
    When the browser sends "POST /api/v1/account/set-feature-previews-enabled" with '{"enabled":true}' through A
    Then it answers 200
    And the answer's account is '{"enabled":true,"canManage":true}'
    And the answer's personal is '{"enabled":false}'
    When "PUT /api/v1/feature-preview/personal" through A with token "p" and '{"enabled":true}' answers '{"features":[],"account":{"enabled":true,"canManage":true},"personal":{"enabled":true},"visible":false}'
    Then "GET /api/v1/feature-preview" through B in the browser eventually answers '{"features":[],"account":{"enabled":true,"canManage":true},"personal":{"enabled":true},"visible":false}'
    And "GET /api/v1/feature-preview" through A with token "admin" eventually answers '{"features":[],"account":{"enabled":true,"canManage":true},"visible":false}'

  @op:featurePreview @op:accountSetFeaturePreviewsEnabled @op:featurePreviewSetPersonalEnabled @case:denial @case:success
  Scenario: A member who is no account admin reads the account's setting, may not change it, and sets their own
    Given the home account's member has a browser session, admitted to home alone
    And the home account's member is "alice"
    When the browser sends "POST /api/v1/personal-api-keys" with '{"name":"laptop"}' through A
    Then the answer's key is the key "laptop"
    And the key "laptop" exchanges through B for token "p"
    And token "admin" sends "GET /api/v1/managed-users/{alice}/user-role-bindings" through A
    And the answer's binding of the role "space-admin" names "top"
    And token "admin" sends "DELETE /api/v1/user-role-bindings/{top}" through B
    Then "GET /api/v1/feature-preview" through A with token "p" eventually answers '{"features":[],"account":{"enabled":false,"canManage":false},"personal":{"enabled":true},"visible":false}'
    When token "p" sends "POST /api/v1/account/set-feature-previews-enabled" with '{"enabled":true}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"ACCOUNT_ADMIN"'
    When the browser sends "POST /api/v1/account/set-feature-previews-enabled" with '{"enabled":true}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"ACCOUNT_ADMIN"'
    And "PUT /api/v1/feature-preview/personal" through B with token "p" and '{"enabled":false}' answers '{"features":[],"account":{"enabled":false,"canManage":false},"personal":{"enabled":false},"visible":false}'
    And "GET /api/v1/feature-preview" through A in the browser answers '{"features":[],"account":{"enabled":false,"canManage":false},"personal":{"enabled":false},"visible":false}'
    And "GET /api/v1/feature-preview" through B with token "admin" answers '{"features":[],"account":{"enabled":false,"canManage":true},"visible":false}'

  @op:featurePreview @op:accountSetFeaturePreviewsEnabled @op:featurePreviewSetPersonalEnabled @case:denial
  Scenario: An API key that is not personal reads no personal setting and sets none, and one that does not administer the account changes nothing
    Given the member key is exchanged through A for token "m"
    And token "admin" sends "POST /api/v1/api-key-role-bindings" with '{"apiKeyID":"01KACME000000000000000000A","roleID":"space-writer","spaceID":"acme"}' through A
    Then "GET /api/v1/feature-preview" through B with token "m" answers '{"features":[],"account":{"enabled":false,"canManage":false},"visible":false}'
    When token "m" sends "POST /api/v1/account/set-feature-previews-enabled" with '{"enabled":true}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"ACCOUNT_ADMIN"'
    When token "m" sends "PUT /api/v1/feature-preview/personal" with '{"enabled":false}' through B
    Then it answers a 403 problem "forbidden"
    When token "admin" sends "PUT /api/v1/feature-preview/personal" with '{"enabled":false}' through A
    Then it answers a 403 problem "forbidden"
    And "GET /api/v1/feature-preview" through B with token "admin" answers '{"features":[],"account":{"enabled":false,"canManage":true},"visible":false}'

  @op:featurePreview @op:accountSetFeaturePreviewsEnabled @op:featurePreviewSetPersonalEnabled @case:failure
  Scenario: A malformed setting is refused and changes nothing, and a reused idempotency key answers 422
    Given the home account's member has a browser session, admitted to home alone
    Then "POST /api/v1/account/set-feature-previews-enabled" through A with token "admin" and '{"enabled":"yes"}' answers a 400 problem "invalid-request"
    And "POST /api/v1/account/set-feature-previews-enabled" through B with token "admin" and '{"enabled":true,"organization":true}' answers a 400 problem "invalid-request"
    And "POST /api/v1/account/set-feature-previews-enabled" through A with token "admin" and '{}' answers a 400 problem "invalid-request"
    When the browser sends "PUT /api/v1/feature-preview/personal" with '{"enabled":"no"}' through B
    Then it answers a 400 problem "invalid-request"
    When the browser sends "PUT /api/v1/feature-preview/personal" with '{"enabled":false,"member":"alice"}' through A
    Then it answers a 400 problem "invalid-request"
    And "GET /api/v1/feature-preview" through B in the browser answers '{"features":[],"account":{"enabled":false,"canManage":true},"personal":{"enabled":true},"visible":false}'
    When token "admin" sends "POST /api/v1/account/set-feature-previews-enabled" with '{"enabled":true}' and the idempotency key "opt-in" through A
    Then it answers 200
    When token "admin" sends "POST /api/v1/account/set-feature-previews-enabled" with '{"enabled":false}' and the idempotency key "opt-in" through B
    Then it answers a 422 problem "idempotency-key-reuse"
    And "GET /api/v1/feature-preview" through B with token "admin" eventually answers '{"features":[],"account":{"enabled":true,"canManage":true},"visible":false}'

  @op:featurePreview @op:accountSetFeaturePreviewsEnabled @op:featurePreviewSetPersonalEnabled @case:failure
  Scenario: A member's session that no account admits reads and sets no feature preview setting
    Given a member no account admits has a browser session
    When the browser sends "GET /api/v1/feature-preview" through A
    Then it answers a 401 problem "unauthenticated"
    When the browser sends "POST /api/v1/account/set-feature-previews-enabled" with '{"enabled":true}' through B
    Then it answers a 401 problem "unauthenticated"
    When the browser sends "PUT /api/v1/feature-preview/personal" with '{"enabled":false}' through A
    Then it answers a 401 problem "unauthenticated"
