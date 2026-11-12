@postgres
Feature: Who holds a space
  A space admin sees who can act in their space and the grant behind each:
  the managed users, the managed user groups and the API keys whose
  bindings reach it, and the users and keys whose live sessions do. A
  grant bound on the space is RESIDENT, one bound on an ancestor
  PROPAGATED, and one bound on a descendant whose path up inherits
  INHERITED, which grants space-reader whatever role it binds. A user
  holds the grants of the groups its latest sign-in reported. Each view
  searches its holders alone, so its suggestions count no one else. A
  space the caller cannot read answers 404 and one it reads but does not
  administer 403. The home key, 01KACME0000000000000000003, and the home
  account's member, alice, are space-admin on the account's top space,
  acme; the member key, 01KACME000000000000000000A, holds only the
  bindings a scenario gives it.

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"

  @op:usersWithAccessToSpace @op:usersWithAccessToSpaceSuggestions @op:userSessionsWithAccessToSpace @op:userSessionsWithAccessToSpaceSuggestions @case:success
  Scenario: A user holds a space by propagation from an ancestor and by inheritance from a descendant
    Given the home account's member has a browser session, admitted to home alone
    And the home account's member is "alice"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Child","description":"","inheritEntities":true,"parentSpace":"legacy"}' through A
    And the answer's id is the space "child"
    When token "h" lists the users with access to the space "legacy" through B
    Then the answer lists 1 holder
    And the holder "{alice}" holds the role "space-admin" on the space "acme" as PROPAGATED
    When token "h" lists the user sessions with access to the space "legacy" through A
    Then the answer lists 1 holder
    And the holder "alice" holds the role "space-admin" through 1 live session
    Given token "h" sends "GET /api/v1/managed-users/{alice}/user-role-bindings" through A
    And the answer's binding of the role "space-admin" names "admin"
    And token "h" sends "DELETE /api/v1/user-role-bindings/{admin}" through B
    And token "h" sends "POST /api/v1/user-role-bindings" with '{"userID":"{alice}","roleID":"space-writer","spaceID":"{child}"}' through A
    When token "h" lists the users with access to the space "legacy" through A
    Then the answer lists 1 holder
    And the holder "{alice}" holds the role "space-reader" on the space "{child}" as INHERITED
    When token "h" lists the users with access to the space "{child}" searching "ali" through B
    Then the holder "{alice}" holds the role "space-writer" on the space "{child}" as RESIDENT
    When token "h" lists the users with access to the space "{child}" searching "nobody" through B
    Then the answer lists 0 holders
    When token "h" asks for the suggestions of the users with access to the space "legacy" through A
    Then the suggestions count 1
    When token "h" asks for the suggestions of the users with access to the space "acme" through A
    Then the suggestions count 0
    When token "h" lists the user sessions with access to the space "acme" through B
    Then the answer lists 0 holders
    When token "h" asks for the suggestions of the user sessions with access to the space "legacy" through B
    Then the suggestions count 1

  @op:userGroupsWithAccessToSpace @op:userGroupsWithAccessToSpaceSuggestions @op:usersWithAccessToSpace @case:success
  Scenario: A group bound on the space holds it, and so does each user its latest sign-in reported it to
    Given the home account's member has a browser session, admitted to home alone
    And the home account's member is "alice"
    And token "h" sends "POST /api/v1/managed-user-groups" with '{"groupName":"ops","roleBindings":[]}' through A
    And the answer's id names "team"
    And token "h" sends "POST /api/v1/user-group-role-bindings" with '{"userGroupID":"{team}","roleID":"space-writer","spaceID":"legacy"}' through B
    When token "h" lists the user groups with access to the space "legacy" through A
    Then the answer lists 1 holder
    And the holder "{team}" holds the role "space-writer" on the space "legacy" as RESIDENT
    When token "h" lists the user groups with access to the space "acme" through B
    Then the answer lists 0 holders
    When token "h" asks for the suggestions of the user groups with access to the space "legacy" through A
    Then the suggestions count 1
    Given the home account's member's latest sign-in to the home account reported the groups "ops,dev"
    When token "h" lists the users with access to the space "legacy" through B
    Then the holder "{alice}" holds the role "space-writer" on the space "legacy" as RESIDENT
    And the holder "{alice}" holds the role "space-admin" on the space "acme" as PROPAGATED

  @op:apiKeysWithAccessToSpace @op:apiKeysWithAccessToSpaceSuggestions @op:apiKeySessionsWithAccessToSpace @op:apiKeySessionsWithAccessToSpaceSuggestions @case:success
  Scenario: An API key's binding holds the space, and its live session is listed under the key's sessions
    Given token "h" sends "POST /api/v1/api-key-role-bindings" with '{"apiKeyID":"01KACME000000000000000000A","roleID":"space-reader","spaceID":"legacy"}' through A
    When token "h" lists the API keys with access to the space "legacy" ordered by "name" through B
    Then the answer lists 2 holders
    And the holder "01KACME000000000000000000A" holds the role "space-reader" on the space "legacy" as RESIDENT
    And the holder "01KACME0000000000000000003" holds the role "space-admin" on the space "acme" as PROPAGATED
    When token "h" lists the API keys with access to the space "acme" through A
    Then the answer lists 1 holder
    When token "h" asks for the suggestions of the API keys with access to the space "legacy" through B
    Then the suggestions count 2
    When token "h" lists the API key sessions with access to the space "legacy" through A
    Then the answer lists 2 holders
    And the holder "01KACME000000000000000000A" holds the role "space-reader" through 1 live session
    When token "h" asks for the suggestions of the API key sessions with access to the space "acme" through B
    Then the suggestions count 1

  @op:usersWithAccessToSpace @op:usersWithAccessToSpaceSuggestions @op:userGroupsWithAccessToSpace @op:userGroupsWithAccessToSpaceSuggestions @op:apiKeysWithAccessToSpace @op:apiKeysWithAccessToSpaceSuggestions @op:userSessionsWithAccessToSpace @op:userSessionsWithAccessToSpaceSuggestions @op:apiKeySessionsWithAccessToSpace @op:apiKeySessionsWithAccessToSpaceSuggestions @case:denial
  Scenario: A reader of a space who does not administer it is refused its views, and a space it cannot read is not found
    Given token "h" sends "POST /api/v1/api-key-role-bindings" with '{"apiKeyID":"01KACME000000000000000000A","roleID":"space-reader","spaceID":"legacy"}' through A
    When token "m" lists the users with access to the space "legacy" through A
    Then it answers a 403 problem "forbidden"
    When token "m" asks for the suggestions of the users with access to the space "legacy" through B
    Then it answers a 403 problem "forbidden"
    When token "m" lists the user groups with access to the space "legacy" through A
    Then it answers a 403 problem "forbidden"
    When token "m" asks for the suggestions of the user groups with access to the space "legacy" through B
    Then it answers a 403 problem "forbidden"
    When token "m" lists the API keys with access to the space "legacy" through A
    Then it answers a 403 problem "forbidden"
    When token "m" asks for the suggestions of the API keys with access to the space "legacy" through B
    Then it answers a 403 problem "forbidden"
    When token "m" lists the user sessions with access to the space "legacy" through A
    Then it answers a 403 problem "forbidden"
    When token "m" asks for the suggestions of the user sessions with access to the space "legacy" through B
    Then it answers a 403 problem "forbidden"
    When token "m" lists the API key sessions with access to the space "legacy" through A
    Then it answers a 403 problem "forbidden"
    When token "m" asks for the suggestions of the API key sessions with access to the space "legacy" through B
    Then it answers a 403 problem "forbidden"
    When token "m" lists the users with access to the space "acme" through B
    Then it answers a 404 problem "not-found"
    When token "m" asks for the suggestions of the API keys with access to the space "acme" through A
    Then it answers a 404 problem "not-found"

  @op:usersWithAccessToSpace @op:usersWithAccessToSpaceSuggestions @op:userGroupsWithAccessToSpace @op:userGroupsWithAccessToSpaceSuggestions @op:apiKeysWithAccessToSpace @op:apiKeysWithAccessToSpaceSuggestions @op:userSessionsWithAccessToSpace @op:userSessionsWithAccessToSpaceSuggestions @op:apiKeySessionsWithAccessToSpace @op:apiKeySessionsWithAccessToSpaceSuggestions @case:failure
  Scenario: A space the account lacks is not found, and a field the view does not order by is refused
    When token "h" lists the users with access to the space "nowhere" through A
    Then it answers a 404 problem "not-found"
    When token "h" asks for the suggestions of the users with access to the space "nowhere" through B
    Then it answers a 404 problem "not-found"
    When token "h" lists the user groups with access to the space "nowhere" through A
    Then it answers a 404 problem "not-found"
    When token "h" asks for the suggestions of the user groups with access to the space "nowhere" through B
    Then it answers a 404 problem "not-found"
    When token "h" lists the API keys with access to the space "nowhere" through A
    Then it answers a 404 problem "not-found"
    When token "h" asks for the suggestions of the API keys with access to the space "nowhere" through B
    Then it answers a 404 problem "not-found"
    When token "h" lists the user sessions with access to the space "nowhere" through A
    Then it answers a 404 problem "not-found"
    When token "h" asks for the suggestions of the user sessions with access to the space "nowhere" through B
    Then it answers a 404 problem "not-found"
    When token "h" lists the API key sessions with access to the space "nowhere" through A
    Then it answers a 404 problem "not-found"
    When token "h" asks for the suggestions of the API key sessions with access to the space "nowhere" through B
    Then it answers a 404 problem "not-found"
    When token "h" lists the users with access to the space "legacy" ordered by "email" through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/orderBy/0/field"
