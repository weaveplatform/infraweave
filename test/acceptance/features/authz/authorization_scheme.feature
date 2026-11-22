@identity @postgres @authorization-scheme
Feature: An account changes its authorization strategy without losing saved configuration
  The account starts with managed users. Changing its strategy preserves both
  configurations, the caller's current administrative session and no other
  account session. The same caller can change back through either replica.
  ref: decisions 0006 and 0007

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"

  @op:authorizationScheme @case:success
  Scenario: An account with no changed settings uses managed users on both replicas
    When token "h" sends "GET /api/v1/authorization-scheme" through A
    Then it answers 200
    And the answer is '"MANAGED_USERS"'
    When token "h" sends "GET /api/v1/authorization-scheme" through B
    Then it answers 200
    And the answer is '"MANAGED_USERS"'

  @op:accountUpdateAuthorizationScheme @op:authorizationScheme @case:success
  Scenario: The current administrator changes strategies and remains able to change back
    When token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through A
    Then it answers 200
    And the answer is '"LOGIN_POLICIES"'
    When token "h" sends "GET /api/v1/authorization-scheme" through B
    Then it answers 200
    And the answer is '"LOGIN_POLICIES"'
    When token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"MANAGED_USERS"}' through B
    Then it answers 200
    And the answer is '"MANAGED_USERS"'
    When token "h" sends "GET /api/v1/authorization-scheme" through A
    Then it answers 200
    And the answer is '"MANAGED_USERS"'

  @op:accountUpdateAuthorizationScheme @case:denial
  Scenario: An authenticated key without account administration cannot change the strategy
    Given the member key is exchanged through B for token "m"
    When token "m" sends "GET /api/v1/authorization-scheme" through B
    Then it answers 200
    And the answer is '"MANAGED_USERS"'
    When token "m" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"ACCOUNT_ADMIN"'
    When token "h" sends "GET /api/v1/authorization-scheme" through B
    Then it answers 200
    And the answer is '"MANAGED_USERS"'

  @op:accountUpdateAuthorizationScheme @case:failure
  Scenario: An unknown strategy is refused without changing the account
    When token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"UNKNOWN"}' through A
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "GET /api/v1/authorization-scheme" through B
    Then it answers 200
    And the answer is '"MANAGED_USERS"'

  @op:accountUpdateAuthorizationScheme @case:success
  Scenario: A switch revokes cached peer sessions across replicas while other accounts stay usable
    Given the home key is exchanged through B for token "peer"
    And the sibling key is exchanged through B for token "other"
    And the home account's member has a browser session, admitted to home alone
    And the viewer through B with token "peer" answers 200
    And the viewer through B in the browser answers 200
    When token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through A
    Then it answers 200
    And the viewer through B with token "peer" answers 401 within a second
    And the viewer through A with token "peer" answers 401 within a second
    And the viewer through B in the browser answers 401
    And the viewer through A with token "other" answers 200
    And the home account has exactly 1 live sessions and 1 retained scheme grants
    And the account records exactly 1 credential-free scheme audit and outbox events
    When token "other" sends "GET /api/v1/authorization-scheme" through B
    Then it answers 200
    And the answer is '"MANAGED_USERS"'

  @op:accountUpdateAuthorizationScheme @case:success
  Scenario: Switching in both directions preserves saved policy and managed authorization rows
    Given the home account's member has a browser session, admitted to home alone
    And the member key is exchanged through B for token "ordinary"
    And the member key is space-reader on the space "acme"
    And token "h" sends "POST /api/v1/managed-user-groups" with '{"groupName":"acceptance","roleBindings":[]}' through B
    And it answers 201
    And the answer's id names "team"
    And token "h" sends "POST /api/v1/user-group-role-bindings" with '{"userGroupID":"{team}","roleID":"space-writer","spaceID":"legacy"}' through A
    And it answers 201
    And token "h" sends "POST /api/v1/policies" with '{"name":"Saved login","type":"LOGIN","body":"package infraweave\nallow := true\n"}' through B
    And it answers 201
    And token "h" sends "POST /api/v1/account/update-security-email" with '{"securityEmail":"scheme-test@example.com"}' through A
    And it answers 200
    And the inactive authorization configuration is recorded
    When token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through A
    Then it answers 200
    And the saved authorization configuration is unchanged
    When token "h" lists the API keys with access to the space "acme" through B
    Then the answer lists 1 holder
    And the holder "01KACME0000000000000000003" holds the role "space-admin" on the space "acme" as RESIDENT
    When token "h" lists the API key sessions with access to the space "acme" through B
    Then the answer lists 1 holder
    And the session view names exactly the space-admin origins "ASSIGNED_ROLE,SCHEME_SWITCH"
    When token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"MANAGED_USERS"}' through B
    Then it answers 200
    And the saved authorization configuration is unchanged
    Given the member key is exchanged through B for token "m"
    Then token "m"'s read of the space "legacy" through A allows exactly the role "space-writer"'s actions
    And the account records exactly 2 credential-free scheme audit and outbox events
    When token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through A
    Then it answers 200
    And the saved authorization configuration is unchanged

  @op:accountUpdateAuthorizationScheme @case:success @case:failure
  Scenario: Two simultaneous administrators cannot preserve two sessions through one switch
    Given the home key is exchanged through B for token "peer"
    And the viewer through A with token "h" answers 200
    And the viewer through B with token "peer" answers 200
    When tokens "h" and "peer" concurrently select LOGIN_POLICIES through A and B
    Then exactly one scheme switch succeeds and the other caller is refused
    And the home account has exactly 1 live sessions and 1 retained scheme grants
    And the account records exactly 1 credential-free scheme audit and outbox events

  @op:accountUpdateAuthorizationScheme @case:success
  Scenario: A real idempotent switch replays across replicas and a current-value write has no security effects
    Given token "h" selects "LOGIN_POLICIES" with idempotency key "scheme-one" through A
    And it answers 200
    When token "h" selects "LOGIN_POLICIES" with idempotency key "scheme-one" through B
    Then it answers 200
    And the scheme switch response was replayed
    And the account records exactly 1 credential-free scheme audit and outbox events
    When token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through B
    Then it answers 200
    And the account records exactly 1 credential-free scheme audit and outbox events

  @op:accountUpdateAuthorizationScheme @case:denial
  Scenario: Administration retained for the switching caller does not transfer to a new session
    Given token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through A
    And it answers 200
    And token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"MANAGED_USERS"}' through B
    And it answers 200
    And token "h" sends "GET /api/v1/api-keys/01KACME0000000000000000003/api-key-role-bindings" through A
    And the answer's binding of the role "space-admin" names "admin"
    When token "h" sends "DELETE /api/v1/api-key-role-bindings/{admin}" through B
    Then it answers 200
    Given the existing home key is exchanged without reseeding through B for token "new"
    When token "new" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through A
    Then it answers a 403 problem "forbidden"
    When token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through B
    Then it answers 200
    And the viewer through A with token "new" answers 401 within a second

  @op:authorizationScheme @op:accountUpdateAuthorizationScheme @case:success
  Scenario: A different organization changes only its own account and sessions
    Given the stranger key is exchanged through B for token "stranger"
    When token "stranger" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through A
    Then it answers 200
    When token "h" sends "GET /api/v1/authorization-scheme" through B
    Then it answers 200
    And the answer is '"MANAGED_USERS"'
    And the viewer through A with token "h" answers 200
    When token "stranger" sends "GET /api/v1/authorization-scheme" through B
    Then it answers 200
    And the answer is '"LOGIN_POLICIES"'
    And the account records exactly 0 credential-free scheme audit and outbox events

  @op:accountUpdateAuthorizationScheme @case:failure
  Scenario Outline: Malformed and cross-account requests cannot change any strategy
    When token "h" sends "POST /api/v1/account/update-authorization-scheme" with '<body>' through A
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "GET /api/v1/authorization-scheme" through B
    Then it answers 200
    And the answer is '"MANAGED_USERS"'
    And the account records exactly 0 credential-free scheme audit and outbox events

    Examples:
      | body |
      | {} |
      | {"schema":null} |
      | {"schema":true} |
      | {"schema":"LOGIN_POLICIES","accountID":"01KG10BEX00000000000000002"} |

  @op:accountUpdateAuthorizationScheme @case:success
  Scenario: A real browser session remains administrative while switching in both directions
    Given the home account's member has a browser session, admitted to home alone
    When the browser sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through A
    Then it answers 200
    And the viewer through B with token "h" answers 401 within a second
    And the viewer through B in the browser answers 200
    When the browser sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"MANAGED_USERS"}' through B
    Then it answers 200
    When the browser sends "GET /api/v1/authorization-scheme" through A
    Then it answers 200
    And the answer is '"MANAGED_USERS"'
    And the account records exactly 2 credential-free scheme audit and outbox events
