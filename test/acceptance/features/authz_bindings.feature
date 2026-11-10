@postgres
Feature: Role bindings
  A role binding grants a role's actions to a managed user, a managed user
  group or an API key in a space and every space below it. A group's
  binding reaches each session that reports the group's name among its
  teams; the member key's sessions report "acceptance". Creating,
  changing or deleting a binding needs SPACE_ADMIN on its space, so an
  admin of a space below the top binds only there: a space they cannot
  read answers 404, and one they read but do not administer 403, or 422
  at its pointer for an element of a batch or the new space of a change.
  A batch writes all of its bindings or none. The access rules and role
  bindings that managed users, groups and API keys carry are stored as
  role bindings; access rules read back as the bindings they made.
  Removing an actor removes its bindings. The home key,
  01KACME0000000000000000003, and the home account's member are
  space-admin on the account's top space, acme; the member key,
  01KACME000000000000000000A, holds only the bindings a scenario gives it.
  An API key never manages API keys, so the member's browser session
  creates them.

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"

  @op:userRoleBindingCreate @op:userRoleBinding @op:userRoleBindingUpdate @op:userRoleBindingPatch @op:userRoleBindingDelete @op:managedUserUserRoleBindings @case:success
  Scenario: A managed user's binding grants exactly its role's actions in its space and the spaces below
    Given the home account's member has a browser session, admitted to home alone
    And the home account's member is "alice"
    And token "h" sends "GET /api/v1/managed-users/{alice}/user-role-bindings" through A
    And the answer's binding of the role "space-admin" names "admin"
    And token "h" sends "DELETE /api/v1/user-role-bindings/{admin}" through B
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Child","description":"","inheritEntities":true,"parentSpace":"legacy"}' through A
    And the answer's id is the space "child"
    When token "h" sends "POST /api/v1/user-role-bindings" with '{"userID":"{alice}","roleID":"space-reader","spaceID":"legacy"}' through A
    Then it answers 201
    And the answer's id names "b"
    And the answer's userID is '"{alice}"'
    And the browser's read of the space "legacy" through B allows exactly the role "space-reader"'s actions
    And the browser's read of the space "{child}" through A allows exactly the role "space-reader"'s actions
    When the browser sends "GET /api/v1/spaces/acme" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/user-role-bindings/{b}" through B
    Then it answers 200
    And the answer's ETag is kept as "first"
    When token "h" sends "PUT /api/v1/user-role-bindings/{b}" with '{"userID":"{alice}","roleID":"space-writer","spaceID":"legacy"}' if matching the ETag "first" through A
    Then it answers 200
    And the browser's read of the space "legacy" through B allows exactly the role "space-writer"'s actions
    When token "h" sends "PATCH /api/v1/user-role-bindings/{b}" with '{"spaceID":"acme"}' through B
    Then it answers 200
    And the browser's read of the space "acme" through A allows exactly the role "space-writer"'s actions
    When token "h" sends "GET /api/v1/managed-users/{alice}/user-role-bindings" through A
    Then the answer lists 1 binding
    When token "h" sends "DELETE /api/v1/user-role-bindings/{b}" through B
    Then it answers 200
    When the browser sends "GET /api/v1/spaces/acme" through A
    Then it answers a 404 problem "not-found"

  @op:userGroupRoleBindingCreate @op:userGroupRoleBinding @op:userGroupRoleBindingUpdate @op:userGroupRoleBindingPatch @op:userGroupRoleBindingDelete @op:userGroupRoleBindingBatchCreate @op:managedUserGroupUserRoleBindings @case:success
  Scenario: A group's binding of a custom role grants exactly its actions to a session reporting the group
    Given token "h" sends "POST /api/v1/roles" with '{"name":"Deployers","actions":["SPACE_READ","STACK_MANAGE"]}' through A
    And token "h" sends "POST /api/v1/managed-user-groups" with '{"groupName":"acceptance","roleBindings":[]}' through B
    And the answer's id names "team"
    When token "h" sends "POST /api/v1/user-group-role-bindings" with '{"userGroupID":"{team}","roleID":"deployers","spaceID":"legacy"}' through A
    Then it answers 201
    And the answer's id names "g"
    And token "m"'s read of the space "legacy" through B allows exactly the role "deployers"'s actions
    When token "h" sends "GET /api/v1/user-group-role-bindings/{g}" through A
    Then it answers 200
    When token "h" sends "PUT /api/v1/user-group-role-bindings/{g}" with '{"userGroupID":"{team}","roleID":"space-reader","spaceID":"legacy"}' through B
    Then it answers 200
    And token "m"'s read of the space "legacy" through A allows exactly the role "space-reader"'s actions
    When token "h" sends "PATCH /api/v1/user-group-role-bindings/{g}" with '{"roleID":"deployers"}' through A
    Then it answers 200
    And token "m"'s read of the space "legacy" through B allows exactly the role "deployers"'s actions
    When token "h" sends "POST /api/v1/user-group-role-binding-batches" with '{"bindings":[{"userGroupID":"{team}","roleID":"space-writer","spaceID":"acme"}]}' through B
    Then it answers 201
    And the answer lists 1 binding
    And token "m"'s read of the space "acme" through A allows exactly the role "space-writer"'s actions
    When token "h" sends "GET /api/v1/managed-user-groups/{team}/user-role-bindings" through A
    Then the answer lists 2 bindings
    When token "h" sends "DELETE /api/v1/user-group-role-bindings/{g}" through B
    Then it answers 200

  @op:apiKeyRoleBindingCreate @op:apiKeyRoleBinding @op:apiKeyRoleBindingUpdate @op:apiKeyRoleBindingPatch @op:apiKeyRoleBindingDelete @op:apiKeyRoleBindingBatchCreate @op:apiKeyApiKeyRoleBindings @op:apiKey @case:success
  Scenario: An API key's binding grants exactly its role's actions, and space-admin on the top space makes it an admin
    When token "h" sends "POST /api/v1/api-key-role-bindings" with '{"apiKeyID":"01KACME000000000000000000A","roleID":"space-writer","spaceID":"legacy"}' through A
    Then it answers 201
    And the answer's id names "k"
    And token "m"'s read of the space "legacy" through B allows exactly the role "space-writer"'s actions
    When token "h" sends "GET /api/v1/api-key-role-bindings/{k}" through A
    Then it answers 200
    When token "h" sends "PUT /api/v1/api-key-role-bindings/{k}" with '{"apiKeyID":"01KACME000000000000000000A","roleID":"space-reader","spaceID":"legacy"}' through B
    Then it answers 200
    And token "m"'s read of the space "legacy" through A allows exactly the role "space-reader"'s actions
    When token "h" sends "PATCH /api/v1/api-key-role-bindings/{k}" with '{"roleID":"space-admin"}' through A
    Then it answers 200
    When token "h" sends "GET /api/v1/api-keys/01KACME000000000000000000A" through B
    Then the answer's admin is 'false'
    And the answer's spaceCount is '1'
    When token "h" sends "POST /api/v1/api-key-role-binding-batches" with '{"bindings":[{"apiKeyID":"01KACME000000000000000000A","roleID":"space-admin","spaceID":"acme"}]}' through B
    Then it answers 201
    When token "h" sends "GET /api/v1/api-keys/01KACME000000000000000000A" through A
    Then the answer's admin is 'true'
    When token "h" sends "GET /api/v1/api-keys/01KACME000000000000000000A/api-key-role-bindings" through B
    Then the answer lists 2 bindings
    When token "h" sends "DELETE /api/v1/api-key-role-bindings/{k}" through A
    Then it answers 200
    When token "h" sends "GET /api/v1/api-keys/01KACME000000000000000000A/api-key-role-bindings" through B
    Then the answer lists 1 binding

  @op:userRoleBindingCreate @op:userRoleBinding @op:userRoleBindingUpdate @op:userRoleBindingPatch @op:userRoleBindingDelete @op:userRoleBindingBatchCreate @op:managedUserUserRoleBindings @op:userGroupRoleBindingCreate @op:userGroupRoleBinding @op:userGroupRoleBindingUpdate @op:userGroupRoleBindingPatch @op:userGroupRoleBindingDelete @op:userGroupRoleBindingBatchCreate @op:managedUserGroupUserRoleBindings @op:apiKeyRoleBindingCreate @op:apiKeyRoleBinding @op:apiKeyRoleBindingUpdate @op:apiKeyRoleBindingPatch @op:apiKeyRoleBindingDelete @op:apiKeyRoleBindingBatchCreate @op:apiKeyApiKeyRoleBindings @op:apiKeys @op:apiKeyEnableNonAdminManagers @case:denial
  Scenario: An admin of a space below the top binds only there
    Given the home account's member has a browser session, admitted to home alone
    And the home account's member is "alice"
    And token "h" sends "POST /api/v1/managed-user-groups" with '{"groupName":"ops","roleBindings":[]}' through A
    And the answer's id names "team"
    And token "h" sends "POST /api/v1/user-role-bindings" with '{"userID":"{alice}","roleID":"space-reader","spaceID":"acme"}' through A
    And the answer's id names "u"
    And token "h" sends "POST /api/v1/user-group-role-bindings" with '{"userGroupID":"{team}","roleID":"space-reader","spaceID":"acme"}' through B
    And the answer's id names "g"
    And token "h" sends "POST /api/v1/api-key-role-bindings" with '{"apiKeyID":"01KACME0000000000000000003","roleID":"space-writer","spaceID":"acme"}' through A
    And the answer's id names "k"
    When token "m" sends "GET /api/v1/managed-users/{alice}/user-role-bindings" through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "GET /api/v1/managed-user-groups/{team}/user-role-bindings" through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "GET /api/v1/api-keys/01KACME0000000000000000003/api-key-role-bindings" through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "GET /api/v1/api-keys" through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PUT /api/v1/account/api-key-non-admin-managers" with '{"enable":true}' through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/user-role-binding-batches" with '{"bindings":[{"userID":"{alice}","roleID":"space-reader","spaceID":"legacy"}]}' through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/user-group-role-binding-batches" with '{"bindings":[{"userGroupID":"{team}","roleID":"space-reader","spaceID":"legacy"}]}' through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/api-key-role-binding-batches" with '{"bindings":[{"apiKeyID":"01KACME0000000000000000003","roleID":"space-reader","spaceID":"legacy"}]}' through B
    Then it answers a 403 problem "forbidden"
    Given the member key is space-admin on the space "legacy"
    When token "m" sends "POST /api/v1/api-key-role-bindings" with '{"apiKeyID":"01KACME0000000000000000003","roleID":"space-reader","spaceID":"legacy"}' through A
    Then it answers 201
    When token "m" sends "POST /api/v1/user-role-bindings" with '{"userID":"{alice}","roleID":"space-reader","spaceID":"acme"}' through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/user-role-bindings/{u}" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/user-group-role-bindings/{g}" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/api-key-role-bindings/{k}" through A
    Then it answers a 404 problem "not-found"
    Given the member key is space-reader on the space "acme"
    When token "m" sends "POST /api/v1/user-role-bindings" with '{"userID":"{alice}","roleID":"space-writer","spaceID":"acme"}' through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/user-group-role-bindings" with '{"userGroupID":"{team}","roleID":"space-writer","spaceID":"acme"}' through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/api-key-role-bindings" with '{"apiKeyID":"01KACME0000000000000000003","roleID":"space-admin","spaceID":"acme"}' through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PUT /api/v1/user-role-bindings/{u}" with '{"userID":"{alice}","roleID":"space-writer","spaceID":"acme"}' through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PATCH /api/v1/user-group-role-bindings/{g}" with '{"roleID":"space-writer"}' through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PUT /api/v1/user-group-role-bindings/{g}" with '{"userGroupID":"{team}","roleID":"space-writer","spaceID":"acme"}' through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PATCH /api/v1/user-role-bindings/{u}" with '{"roleID":"space-writer"}' through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PUT /api/v1/api-key-role-bindings/{k}" with '{"apiKeyID":"01KACME0000000000000000003","roleID":"space-reader","spaceID":"acme"}' through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PATCH /api/v1/api-key-role-bindings/{k}" with '{"roleID":"space-reader"}' through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "DELETE /api/v1/user-role-bindings/{u}" through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "DELETE /api/v1/user-group-role-bindings/{g}" through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "DELETE /api/v1/api-key-role-bindings/{k}" through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/user-role-binding-batches" with '{"bindings":[{"userID":"{alice}","roleID":"space-reader","spaceID":"legacy"},{"userID":"{alice}","roleID":"space-admin","spaceID":"acme"}]}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/bindings/1/spaceID"
    When token "h" sends "GET /api/v1/managed-users/{alice}/user-role-bindings" through A
    Then the answer lists 2 bindings

  @op:userRoleBindingCreate @op:userRoleBinding @op:userRoleBindingUpdate @op:userRoleBindingPatch @op:userRoleBindingDelete @op:userRoleBindingBatchCreate @op:managedUserUserRoleBindings @op:userGroupRoleBindingCreate @op:userGroupRoleBinding @op:userGroupRoleBindingUpdate @op:userGroupRoleBindingPatch @op:userGroupRoleBindingDelete @op:userGroupRoleBindingBatchCreate @op:managedUserGroupUserRoleBindings @op:apiKeyRoleBindingCreate @op:apiKeyRoleBinding @op:apiKeyRoleBindingUpdate @op:apiKeyRoleBindingPatch @op:apiKeyRoleBindingDelete @op:apiKeyRoleBindingBatchCreate @op:apiKeyApiKeyRoleBindings @case:failure
  Scenario: An unknown actor, role or binding, a duplicate, a changed actor and a batch with one bad element are refused
    Given the home account's member has a browser session, admitted to home alone
    And the home account's member is "alice"
    And token "h" sends "POST /api/v1/managed-user-groups" with '{"groupName":"ops","roleBindings":[]}' through A
    And the answer's id names "team"
    When token "h" sends "POST /api/v1/user-role-bindings" with '{"userID":"nobody","roleID":"nope","spaceID":"legacy"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/userID"
    And the problem refuses the field "/roleID"
    When token "h" sends "POST /api/v1/user-role-bindings" with '{"userID":"{alice}","roleID":"space-reader","spaceID":"legacy"}' through B
    Then it answers 201
    And the answer's id names "u"
    When token "h" sends "POST /api/v1/user-role-bindings" with '{"userID":"{alice}","roleID":"space-reader","spaceID":"legacy"}' through A
    Then it answers a 409 problem "already-exists"
    When token "h" sends "POST /api/v1/user-role-binding-batches" with '{"bindings":[{"userID":"{alice}","roleID":"space-writer","spaceID":"legacy"},{"userID":"{alice}","roleID":"nope","spaceID":"legacy"}]}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/bindings/1/roleID"
    When token "h" sends "POST /api/v1/user-role-binding-batches" with '{"bindings":[{"userID":"{alice}","roleID":"space-writer","spaceID":"legacy"},{"userID":"{alice}","roleID":"space-reader","spaceID":"legacy"}]}' through A
    Then it answers a 409 problem "already-exists"
    And the problem's detail names "/bindings/1"
    When token "h" sends "GET /api/v1/managed-users/{alice}/user-role-bindings" through B
    Then the answer lists 2 bindings
    When token "h" sends "PUT /api/v1/user-role-bindings/{u}" with '{"userID":"01KACME0000000000000000003","roleID":"space-reader","spaceID":"legacy"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/userID"
    When token "h" sends "PATCH /api/v1/user-role-bindings/{u}" with '{"roleID":"nope"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/roleID"
    When token "h" sends "GET /api/v1/user-role-bindings/01KACME0000000000000000099" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "DELETE /api/v1/user-role-bindings/01KACME0000000000000000099" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/managed-users/nobody/user-role-bindings" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/user-group-role-bindings" with '{"userGroupID":"{team}","roleID":"nope","spaceID":"legacy"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/roleID"
    When token "h" sends "POST /api/v1/user-group-role-bindings" with '{"userGroupID":"{team}","roleID":"space-reader","spaceID":"legacy"}' through A
    Then it answers 201
    And the answer's id names "g"
    When token "h" sends "POST /api/v1/user-group-role-binding-batches" with '{"bindings":[{"userGroupID":"nobody","roleID":"space-reader","spaceID":"legacy"}]}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/bindings/0/userGroupID"
    When token "h" sends "PUT /api/v1/user-group-role-bindings/{g}" with '{"userGroupID":"{team}","roleID":"nope","spaceID":"legacy"}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "PATCH /api/v1/user-group-role-bindings/{u}" with '{"roleID":"space-writer"}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/user-group-role-bindings/{u}" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "DELETE /api/v1/user-group-role-bindings/01KACME0000000000000000099" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/managed-user-groups/nobody/user-role-bindings" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/api-key-role-bindings" with '{"apiKeyID":"01KACME0000000000000000099","roleID":"space-reader","spaceID":"legacy"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/apiKeyID"
    When token "h" sends "POST /api/v1/api-key-role-bindings" with '{"apiKeyID":"01KACME000000000000000000A","roleID":"space-reader","spaceID":"legacy"}' through A
    Then it answers 201
    And the answer's id names "k"
    When token "h" sends "POST /api/v1/api-key-role-binding-batches" with '{"bindings":[{"apiKeyID":"01KACME000000000000000000A","roleID":"space-reader","spaceID":"legacy"}]}' through B
    Then it answers a 409 problem "already-exists"
    When token "h" sends "PUT /api/v1/api-key-role-bindings/{k}" with '{"apiKeyID":"01KACME0000000000000000003","roleID":"space-reader","spaceID":"legacy"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/apiKeyID"
    When token "h" sends "PATCH /api/v1/api-key-role-bindings/{k}" with '{"roleID":"nope"}' through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "GET /api/v1/api-key-role-bindings/{u}" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "DELETE /api/v1/api-key-role-bindings/01KACME0000000000000000099" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/api-keys/01KACME0000000000000000099/api-key-role-bindings" through A
    Then it answers a 404 problem "not-found"

  @op:managedUserInviteV2 @op:managedUserUpdate @op:managedUser @op:managedUserDelete @op:managedUserGroupCreateV2 @op:apiKeyCreate @case:success
  Scenario: Access rules and role bindings carried by managed users, groups and API keys are stored as bindings
    Given the home account's member has a browser session, admitted to home alone
    When token "h" sends "POST /api/v1/managed-users" with '{"username":"bob","roleBindings":[{"roleID":"space-reader","spaceID":"legacy"}]}' through A
    Then it answers 201
    And the answer's id names "bob"
    And the answer's accessRules is '[]'
    When token "h" sends "GET /api/v1/managed-users/{bob}/user-role-bindings" through B
    Then the answer lists 1 binding
    When token "h" sends "PUT /api/v1/managed-users/{bob}" with '{"accessRules":[{"space":"acme","spaceAccessLevel":"WRITE"},{"space":"legacy","spaceAccessLevel":"NONE"}]}' through A
    Then it answers 200
    And the answer's accessRules is '[{"space":"acme","spaceAccessLevel":"WRITE"}]'
    When token "h" sends "PATCH /api/v1/managed-users/{bob}" with '{"slackMemberID":"U1"}' through B
    Then it answers 200
    When token "h" sends "GET /api/v1/managed-users/{bob}" through A
    Then the answer's accessRules is '[{"space":"acme","spaceAccessLevel":"WRITE"}]'
    When token "h" sends "GET /api/v1/managed-users/{bob}/user-role-bindings" through B
    Then the answer lists 2 bindings
    When token "h" sends "POST /api/v1/user-role-bindings" with '{"userID":"{bob}","roleID":"space-admin","spaceID":"legacy"}' through A
    Then it answers 201
    And the answer's id names "held"
    When token "h" sends "DELETE /api/v1/managed-users/{bob}" through B
    Then it answers 200
    When token "h" sends "GET /api/v1/user-role-bindings/{held}" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/managed-user-groups" with '{"groupName":"ops","roleBindings":[{"roleID":"space-writer","spaceID":"legacy"}]}' through B
    Then it answers 201
    And the answer's id names "team"
    When token "h" sends "GET /api/v1/managed-user-groups/{team}/user-role-bindings" through A
    Then the answer lists 1 binding
    When the browser sends "POST /api/v1/api-keys" with '{"name":"deploy","teams":[],"accessRules":[{"space":"acme","spaceAccessLevel":"ADMIN"}],"roleBindings":[{"roleID":"space-reader","spaceID":"legacy"}]}' through A
    Then it answers 201
    And the answer's admin is 'true'
    And the answer's spaceCount is '2'
    And the answer's accessRules is '[{"space":"acme","spaceAccessLevel":"ADMIN"}]'
    When the browser sends "POST /api/v1/api-keys" with '{"name":"other","teams":[],"accessRules":[{"space":"nowhere","spaceAccessLevel":"READ"}]}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/accessRules/0/space"
