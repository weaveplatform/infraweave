@postgres
Feature: Managed users and groups
  A managed user is a member of the organization as one account sees it.
  Inviting a login admits the organization's member with it, or creates a
  pending member, and records the invitation without e-mail delivery
  (capture C251); a second invitation for an address with a pending one
  answers 409 invalid-state. Removing a managed user stops the account
  admitting them, and they stay a member of the organization. A member
  counts once against the seats limit however many accounts admit it. Groups
  are each account's: their members are the account's managed users whose
  latest sign-in reported the group's name. The role bindings and access
  rules an input carries are stored as role bindings, and one naming a role
  or space the account lacks answers 422 validation-failed at its pointer.
  Reading users and groups needs MANAGED_USER_READ and
  MANAGED_USER_GROUP_READ, which space-admin on any space holds and
  space-writer on the top space does not; changing them needs the
  change's action on the top space. The member key,
  01KACME000000000000000000A, holds only the bindings a scenario gives it.
  ref: decision 0007

  Background:
    Given two infraweave servers, A and B, on one database, with an operator key
    And the home key is exchanged through A for token "h"

  @op:managedUserInviteV2 @op:managedUsers @op:managedUser @op:managedUserUpdate @op:managedUserDelete @case:success @case:failure
  Scenario: An account invites, lists, reads, updates and removes a managed user
    When token "h" sends "POST /api/v1/managed-users" with '{"username":"bob","invitationEmail":"bob@example.com","roleBindings":[]}' through A
    Then it answers 201
    And the answer's id names "bob"
    And the answer's status is '"PENDING"'
    And the answer's invitationEmail is '"bob@example.com"'
    When token "h" sends "GET /api/v1/managed-users" through B
    Then the answer lists the managed users "bob"
    When token "h" sends "GET /api/v1/managed-users/{bob}" through B
    Then the answer's username is '"bob"'
    When token "h" sends "PUT /api/v1/managed-users/{bob}" with '{"accessRules":[],"slackMemberID":"U123"}' through A
    Then the answer's slackMemberID is '"U123"'
    When token "h" sends "PUT /api/v1/managed-users/{bob}" with '{"accessRules":[{"space":"nowhere","spaceAccessLevel":"READ"}]}' through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "DELETE /api/v1/managed-users/{bob}" through A
    Then the answer's username is '"bob"'
    When token "h" sends "GET /api/v1/managed-users/{bob}" through B
    Then it answers a 404 problem "not-found"
    And the home account's audit trail holds "managed_user.delete"

  @op:managedUserInviteV2 @case:failure
  Scenario: A second invitation for one address, or of one member, is refused
    When token "h" sends "POST /api/v1/managed-users" with '{"username":"bob","invitationEmail":"bob@example.com","roleBindings":[]}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/managed-users" with '{"username":"carol","invitationEmail":"bob@example.com","roleBindings":[]}' through B
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "POST /api/v1/managed-users" with '{"username":"bob","roleBindings":[]}' through A
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "POST /api/v1/managed-users" with '{"username":"dan","roleBindings":[{"roleID":"r","spaceID":"nowhere"}]}' through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "POST /api/v1/managed-users" with '{"username":"","invitationEmail":"nope","roleBindings":[]}' through A
    Then it answers a 422 problem "validation-failed"

  @op:managedUserResendInvite @op:managedUserRequestInvite @op:managedUserRequestRoleChange @op:managedUserPersonalApiKeys @case:success @case:denial
  Scenario: Resends and requests are recorded on the account's trail
    Given the home account's member has a browser session, admitted to home alone
    When token "h" sends "POST /api/v1/managed-users" with '{"username":"bob","roleBindings":[]}' through A
    And the answer's id names "bob"
    And token "h" sends "POST /api/v1/managed-users/{bob}/resend-invite" through B
    Then the answer's status is '"PENDING"'
    When token "h" sends "GET /api/v1/managed-users/{bob}/personal-api-keys" through A
    Then the answer's items is '[]'
    When token "h" sends "POST /api/v1/managed-user-invite-requests" with '{"email":"carol@example.com","userName":"carol"}' through A
    Then it answers 201
    When the browser sends "POST /api/v1/spaces/legacy/role-change-requests" with '{"requestedAccessLevel":"WRITE"}' through B
    Then it answers 201
    When the browser sends "POST /api/v1/spaces/elsewhere/role-change-requests" with '{"requestedAccessLevel":"WRITE"}' through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/spaces/legacy/role-change-requests" with '{"requestedAccessLevel":"READ"}' through A
    Then it answers a 403 problem "forbidden"
    And the home account's audit trail holds "managed_user.resend_invite"
    And the home account's audit trail holds "managed_user.request_invite"
    And the home account's audit trail holds "managed_user.request_role_change"

  @op:managedUserInviteV2 @op:managedUsers @case:success
  Scenario: An invited member appears in the account switcher once admitted
    Given the home account's member has a browser session, admitted to home alone
    And the sibling key is exchanged through A for token "s"
    Then the browser's account switcher through A lists "acme"
    When token "s" sends "POST /api/v1/managed-users" with '{"username":"alice","roleBindings":[]}' through B
    Then the answer's status is '"ACTIVE"'
    And the browser's account switcher through A lists "acme, acme-dev"

  @op:managedUserGroupCreateV2 @op:managedUserGroups @op:managedUserGroup @op:managedUserGroupUpdateV2 @op:managedUserGroupDelete @op:managedUserGroupManagedUsers @op:availableUserGroups @case:success @case:failure
  Scenario: An account's groups match its managed users' identity provider groups
    Given the home account's member has a browser session, admitted to home alone
    And the home account's member's latest sign-in to the home account reported the groups "ops,dev"
    When token "h" sends "POST /api/v1/managed-user-groups" with '{"groupName":"ops","description":"Operators","roleBindings":[]}' through A
    Then it answers 201
    And the answer's id names "ops"
    And the answer's members is '["alice"]'
    When token "h" sends "POST /api/v1/managed-user-groups" with '{"groupName":"ops","roleBindings":[]}' through B
    Then it answers a 409 problem "already-exists"
    When token "h" sends "POST /api/v1/managed-user-groups" with '{"groupName":"qa","roleBindings":[{"roleID":"r","spaceID":"nowhere"}]}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "GET /api/v1/managed-user-groups" through B
    Then it answers 200
    When token "h" sends "PUT /api/v1/managed-user-groups/{ops}" with '{"description":"Operations"}' through A
    Then the answer's description is '"Operations"'
    When token "h" sends "GET /api/v1/managed-user-groups/{ops}/managed-users" through B
    Then the answer lists the managed users "alice"
    When token "h" sends "GET /api/v1/available-user-groups" through A
    Then the answer's items is '["dev","ops"]'
    When token "h" sends "GET /api/v1/managed-users" through B
    Then the answer lists the managed users "alice"
    When token "h" sends "DELETE /api/v1/managed-user-groups/{ops}" through A
    Then the answer's groupName is '"ops"'
    When token "h" sends "GET /api/v1/managed-user-groups/{ops}" through B
    Then it answers a 404 problem "not-found"

  @op:managedUser @op:managedUserGroup @op:managedUserDelete @case:denial
  Scenario: A managed user or group of one account is unknown in another
    Given the sibling key is exchanged through A for token "s"
    When token "h" sends "POST /api/v1/managed-users" with '{"username":"bob","roleBindings":[]}' through A
    And the answer's id names "bob"
    And token "h" sends "POST /api/v1/managed-user-groups" with '{"groupName":"ops","roleBindings":[]}' through A
    And the answer's id names "ops"
    And token "s" sends "GET /api/v1/managed-users/{bob}" through B
    Then it answers a 404 problem "not-found"
    When token "s" sends "DELETE /api/v1/managed-users/{bob}" through A
    Then it answers a 404 problem "not-found"
    When token "s" sends "GET /api/v1/managed-user-groups/{ops}" through B
    Then it answers a 404 problem "not-found"
    When token "s" sends "GET /api/v1/managed-users" through B
    Then the answer lists the managed users ""

  @op:managedUserInviteV2 @case:denial
  Scenario: A member admitted by two accounts holds one seat, and an invite beyond the pool is refused
    Given the operator key is exchanged through A for token "op"
    And token "op" creates the tier "Gold" with the feature "managed-users" and 3 seats through A
    And token "op" gives the home organization the tier "Gold" through A
    And token "op" turns billing on through A
    And the sibling key is exchanged through A for token "s"
    When token "h" sends "POST /api/v1/managed-users" with '{"username":"bob","roleBindings":[]}' through A
    Then it answers 201
    When token "s" sends "POST /api/v1/managed-users" with '{"username":"bob","roleBindings":[]}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/managed-users" with '{"username":"carol","roleBindings":[]}' through A
    Then it answers a 403 problem "limit-reached"
    And the problem names the limit "seats" of the organization at 3

  @op:managedUsers @op:managedUser @op:managedUserUpdate @op:managedUserGroups @op:managedUserGroup @op:managedUserGroupCreateV2 @op:managedUserGroupUpdateV2 @op:managedUserGroupDelete @op:managedUserGroupManagedUsers @op:availableUserGroups @case:denial
  Scenario: A key that writes in the top space but administers no space is refused the account's users and groups, which an admin of a space below the top reads
    Given the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/managed-users" with '{"username":"bob","roleBindings":[]}' through A
    And the answer's id names "bob"
    And token "h" sends "POST /api/v1/managed-user-groups" with '{"groupName":"ops","roleBindings":[]}' through A
    And the answer's id names "ops"
    And token "h" sends "POST /api/v1/api-key-role-bindings" with '{"apiKeyID":"01KACME000000000000000000A","roleID":"space-writer","spaceID":"acme"}' through A
    And the answer's id names "writer"
    When token "m" sends "GET /api/v1/managed-users" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"MANAGED_USER_READ"'
    When token "m" sends "GET /api/v1/managed-users/{bob}" through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PUT /api/v1/managed-users/{bob}" with '{"accessRules":[],"slackMemberID":"U123"}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"MANAGED_USER_UPDATE"'
    When token "m" sends "GET /api/v1/managed-user-groups" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"MANAGED_USER_GROUP_READ"'
    When token "m" sends "GET /api/v1/managed-user-groups/{ops}" through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "GET /api/v1/managed-user-groups/{ops}/managed-users" through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "GET /api/v1/available-user-groups" through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/managed-user-groups" with '{"groupName":"qa","roleBindings":[]}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"MANAGED_USER_GROUP_CREATE"'
    When token "m" sends "PUT /api/v1/managed-user-groups/{ops}" with '{"description":"Mine"}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"MANAGED_USER_GROUP_UPDATE"'
    When token "m" sends "DELETE /api/v1/managed-user-groups/{ops}" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"MANAGED_USER_GROUP_DELETE"'
    When token "h" sends "DELETE /api/v1/api-key-role-bindings/{writer}" through A
    And token "h" sends "POST /api/v1/api-key-role-bindings" with '{"apiKeyID":"01KACME000000000000000000A","roleID":"space-admin","spaceID":"legacy"}' through A
    And token "m" sends "GET /api/v1/managed-users" through A
    Then the answer lists the managed users "bob"
    When token "m" sends "GET /api/v1/managed-user-groups/{ops}" through A
    Then the answer's groupName is '"ops"'
    When token "m" sends "GET /api/v1/managed-user-groups/{ops}/managed-users" through A
    Then it answers 200
    When token "m" sends "GET /api/v1/available-user-groups" through A
    Then it answers 200
    When token "m" sends "PUT /api/v1/managed-users/{bob}" with '{"accessRules":[],"slackMemberID":"U123"}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"MANAGED_USER_UPDATE"'
    When token "m" sends "DELETE /api/v1/managed-user-groups/{ops}" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"MANAGED_USER_GROUP_DELETE"'
