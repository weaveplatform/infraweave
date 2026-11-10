@postgres
Feature: Live sessions follow changes of their grants
  A session's grants are never stored with it: each request is decided on
  the account's authorization snapshot, which every replica rebuilds when
  a change of the account's spaces, roles, bindings, groups or admissions
  commits. So a binding removed, a role's actions changed or a group
  deleted through one replica reaches a member's live session on the
  other within a second, with no new sign-in. An ACTIVE managed user with
  no binding signs in with empty grants: the session is valid and reads
  the viewer and the account, and every space answers 404. The home
  account's member, alice, has a browser session; the member key's
  sessions report the group "acceptance".

  ref: decisions 0005 and 0007, DV-023

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And the home account's member has a browser session, admitted to home alone
    And the home account's member is "alice"
    And token "h" sends "GET /api/v1/managed-users/{alice}/user-role-bindings" through A
    And the answer's binding of the role "space-admin" names "admin"
    And token "h" sends "DELETE /api/v1/user-role-bindings/{admin}" through B

  @op:userRoleBindingDelete @case:success
  Scenario: A binding removed through one replica hides its space from the member's session on the other
    Given token "h" sends "POST /api/v1/user-role-bindings" with '{"userID":"{alice}","roleID":"space-reader","spaceID":"legacy"}' through A
    And the answer's id names "b"
    And the browser's read of the space "legacy" through B allows exactly the role "space-reader"'s actions
    When token "h" sends "DELETE /api/v1/user-role-bindings/{b}" through A
    Then it answers 200
    And the browser's read of the space "legacy" through B is not found
    And the browser's read of the space "legacy" through A is not found

  @op:roleUpdate @case:success
  Scenario: A custom role's actions changed through one replica change the member's allowed actions on the other
    Given token "h" sends "POST /api/v1/roles" with '{"name":"Deployers","actions":["SPACE_READ","STACK_MANAGE"]}' through A
    And token "h" sends "POST /api/v1/user-role-bindings" with '{"userID":"{alice}","roleID":"deployers","spaceID":"legacy"}' through B
    And the browser's read of the space "legacy" through B allows exactly the role "deployers"'s actions
    When token "h" sends "PUT /api/v1/roles/deployers" with '{"name":"Deployers","Actions":["SPACE_READ"]}' through A
    Then it answers 200
    And the browser's read of the space "legacy" through B allows exactly the role "deployers"'s actions

  @op:managedUserGroupDelete @case:success
  Scenario: A group deleted through one replica takes its grants from the sessions reporting it on the other
    Given token "h" sends "POST /api/v1/managed-user-groups" with '{"groupName":"acceptance","roleBindings":[]}' through B
    And the answer's id names "team"
    And token "h" sends "POST /api/v1/user-group-role-bindings" with '{"userGroupID":"{team}","roleID":"space-writer","spaceID":"legacy"}' through A
    And token "m"'s read of the space "legacy" through B allows exactly the role "space-writer"'s actions
    When token "h" sends "DELETE /api/v1/managed-user-groups/{team}" through A
    Then it answers 200
    And token "m"'s read of the space "legacy" through B is not found
    And token "m"'s read of the space "legacy" through A is not found

  @op:viewer @op:managedUser @op:spaces @op:space @case:success
  Scenario: An ACTIVE managed user with no binding signs in with empty grants and sees no space
    When token "h" sends "GET /api/v1/managed-users/{alice}" through A
    Then the answer's status is '"ACTIVE"'
    When token "h" sends "GET /api/v1/managed-users/{alice}/user-role-bindings" through B
    Then the answer lists 0 bindings
    And the browser's read of the space "acme" through A is not found
    And the browser's read of the space "acme" through B is not found
    And the viewer through B in the browser answers 200
    And the account read through A in the browser is the home account
    When the browser sends "GET /api/v1/spaces" through B
    Then the answer lists the spaces ""
    When the browser sends "GET /api/v1/spaces/legacy" through A
    Then it answers a 404 problem "not-found"
