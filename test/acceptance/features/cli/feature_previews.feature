@postgres
Feature: infraweavectl preview
  infraweavectl preview reads the account's feature previews and sets the
  account's opt-in, as an account admin, or the caller's own setting,
  printing each answer as JSON and, for a refusal, its problem. The
  catalogue marks no feature in preview yet, so the list is empty and not
  visible, and both settings are still stored and read back. The home key
  administers the account and names no member; a personal API key of the
  home account's member acts as the member; the member key,
  01KACME000000000000000000A, holds only the bindings a scenario gives it.
  ref: decision 0021

  Background:
    Given two infraweave servers, A and B, on one database
    And infraweavectl's profile "admin" holds A's endpoint and the home key

  @op:featurePreview @op:accountSetFeaturePreviewsEnabled @case:success
  Scenario: An account admin reads the feature previews and opts the account in and out with infraweavectl
    Then infraweavectl "preview show" prints '{"features":[],"account":{"enabled":false,"canManage":true},"visible":false}'
    And infraweavectl "preview account enable" prints '{"features":[],"account":{"enabled":true,"canManage":true},"visible":false}'
    And infraweavectl "preview show" prints '{"features":[],"account":{"enabled":true,"canManage":true},"visible":false}'
    And infraweavectl "preview account disable" prints '{"features":[],"account":{"enabled":false,"canManage":true},"visible":false}'
    And infraweavectl "preview show" prints '{"features":[],"account":{"enabled":false,"canManage":true},"visible":false}'

  @op:featurePreview @op:featurePreviewSetPersonalEnabled @case:success
  Scenario: A member hides feature previews in their own clients and shows them again with infraweavectl and a personal key
    Given the home account's member has a browser session, admitted to home alone
    When the browser sends "POST /api/v1/personal-api-keys" with '{"name":"laptop"}' through A
    Then the answer's key is the key "laptop"
    And the key "laptop" exchanges through B for token "p"
    And infraweavectl's profile "alice" holds B's endpoint and token "p"
    Then infraweavectl "--profile alice preview show" prints '{"features":[],"account":{"enabled":false,"canManage":true},"personal":{"enabled":true},"visible":false}'
    And infraweavectl "--profile alice preview personal disable" prints '{"features":[],"account":{"enabled":false,"canManage":true},"personal":{"enabled":false},"visible":false}'
    And infraweavectl "--profile alice preview show" prints '{"features":[],"account":{"enabled":false,"canManage":true},"personal":{"enabled":false},"visible":false}'
    And infraweavectl "preview show" prints '{"features":[],"account":{"enabled":false,"canManage":true},"visible":false}'
    And infraweavectl "--profile alice preview personal enable" prints '{"features":[],"account":{"enabled":false,"canManage":true},"personal":{"enabled":true},"visible":false}'

  @op:featurePreview @op:accountSetFeaturePreviewsEnabled @op:featurePreviewSetPersonalEnabled @case:denial @case:success
  Scenario: A member who is no account admin may not opt the account in with infraweavectl, and sets their own setting
    Given the home key is exchanged through A for token "admin"
    And the home account's member has a browser session, admitted to home alone
    And the home account's member is "alice"
    When the browser sends "POST /api/v1/personal-api-keys" with '{"name":"laptop"}' through A
    Then the answer's key is the key "laptop"
    And the key "laptop" exchanges through B for token "p"
    And token "admin" sends "GET /api/v1/managed-users/{alice}/user-role-bindings" through A
    And the answer's binding of the role "space-admin" names "top"
    And token "admin" sends "DELETE /api/v1/user-role-bindings/{top}" through B
    And "GET /api/v1/feature-preview" through A with token "p" eventually answers '{"features":[],"account":{"enabled":false,"canManage":false},"personal":{"enabled":true},"visible":false}'
    And infraweavectl's profile "alice" holds A's endpoint and token "p"
    Then infraweavectl "--profile alice preview show" prints '{"features":[],"account":{"enabled":false,"canManage":false},"personal":{"enabled":true},"visible":false}'
    And infraweavectl "--profile alice preview account enable" fails with a 403 problem "forbidden" naming the action "ACCOUNT_ADMIN"
    And infraweavectl "--profile alice preview personal disable" prints '{"features":[],"account":{"enabled":false,"canManage":false},"personal":{"enabled":false},"visible":false}'
    And infraweavectl "preview show" prints '{"features":[],"account":{"enabled":false,"canManage":true},"visible":false}'

  @op:featurePreview @op:accountSetFeaturePreviewsEnabled @op:featurePreviewSetPersonalEnabled @case:denial
  Scenario: An API key that is not personal sets no personal setting with infraweavectl, and one that does not administer the account changes nothing
    Given the home key is exchanged through A for token "admin"
    And the member key is exchanged through A for token "m"
    And token "admin" sends "POST /api/v1/api-key-role-bindings" with '{"apiKeyID":"01KACME000000000000000000A","roleID":"space-writer","spaceID":"acme"}' through A
    And infraweavectl's profile "member" holds B's endpoint and the member key
    Then infraweavectl "--profile member preview show" prints '{"features":[],"account":{"enabled":false,"canManage":false},"visible":false}'
    And infraweavectl "--profile member preview account enable" fails with a 403 problem "forbidden" naming the action "ACCOUNT_ADMIN"
    And infraweavectl "--profile member preview personal disable" fails with a 403 problem "forbidden"
    And infraweavectl "preview personal disable" fails with a 403 problem "forbidden"
    And infraweavectl "preview show" prints '{"features":[],"account":{"enabled":false,"canManage":true},"visible":false}'

  @op:featurePreview @op:accountSetFeaturePreviewsEnabled @op:featurePreviewSetPersonalEnabled @case:failure
  Scenario: infraweavectl prints the problem of a session that has ended, and the account's opt-in is unchanged
    Given the home key is exchanged through A for token "ended"
    And infraweavectl's profile "ended" holds B's endpoint and token "ended"
    When token "ended" logs out through A
    Then the viewer through B with token "ended" answers 401 within a second
    And infraweavectl "--profile ended preview show" fails with a 401 problem "unauthenticated"
    And infraweavectl "--profile ended preview account enable" fails with a 401 problem "unauthenticated"
    And infraweavectl "--profile ended preview personal disable" fails with a 401 problem "unauthenticated"
    And infraweavectl "preview show" prints '{"features":[],"account":{"enabled":false,"canManage":true},"visible":false}'
