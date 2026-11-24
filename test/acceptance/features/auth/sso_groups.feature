@identity @oidc @postgres
Feature: Identity provider groups decide each account's managed user groups
  The groups an OIDC provider reports at sign-in, read through the claim its
  claim mapping names for them (groups by default) as strings, become the
  session's original teams. Entering an account writes them as the latest
  groups of the member's admission to that account alone, in the entry's
  transaction: another account keeps the groups of the member's last entry
  into it until the member enters it again. Under MANAGED_USERS a managed
  user group whose name equals one of them, exactly and case-sensitively,
  holds the member, which gains the group's bindings; a sign-in that reports
  no groups leaves every group at its next entry. A missing claim, values
  that are not strings and Entra's groups overage mean no groups, never a
  refused sign-in. Under LOGIN_POLICIES the login policy reads the groups as
  input.session.teams. The viewer and the managed user report the kind of
  the provider the member last signed in through. Each sign-in is a real
  one through the scenario's in-process issuer.
  ref: decision 0006 § "Human login"
  ref: decision 0007 § "Bindings"

  Background:
    Given two infraweave servers, A and B, on one database, trusting the scenario's OIDC issuers
    And the home organization has the OIDC provider "okta" at the in-process issuer, enabled with the admin login "root"
    And the home key is exchanged through A for token "h"

  @op:managedUserGroupManagedUsers @op:managedUser @op:viewer @case:success
  Scenario: A member's sign-in groups put it in the account's group of the same name, in the account it enters alone
    Given the member "nora" is invited to the home and sibling accounts
    And the sibling key is exchanged through B for token "s"
    And token "h" sends "POST /api/v1/managed-user-groups" with '{"groupName":"platform","roleBindings":[{"roleID":"space-admin","spaceID":"legacy"}]}' through A
    And the answer's id names "platform"
    And token "h" sends "POST /api/v1/managed-user-groups" with '{"groupName":"ops","roleBindings":[]}' through B
    And the answer's id names "ops"
    And the provider "okta" signs in "nora-sub" next with the claims '{"preferred_username":"nora","groups":["platform","Ops"]}'
    When the browser signs in through the provider "okta" at A
    And the browser switches to the home account through B
    And the browser sends "GET /api/v1/viewer" through A
    Then the answer's teams is '["platform","Ops"]'
    And the answer's identityProvider is '"OIDC"'
    When the browser sends "GET /api/v1/spaces/legacy" through B
    Then it answers 200
    When the browser sends "GET /api/v1/managed-user-groups/{platform}/managed-users" through A
    Then the answer lists the managed users "nora"
    When token "h" sends "GET /api/v1/managed-user-groups/{ops}/managed-users" through B
    Then the answer lists the managed users ""
    When token "h" sends "GET /api/v1/managed-users/{nora}" through A
    Then the answer's latestGroups is '["platform","Ops"]'
    And the answer's groupCount is '1'
    And the answer's loginMethod is '"OIDC"'
    When token "s" sends "GET /api/v1/managed-users/{nora}" through B
    Then the answer's latestGroups is '[]'
    And the answer's loginMethod is '"OIDC"'
    When the browser switches to the sibling account through A
    And token "s" sends "GET /api/v1/managed-users/{nora}" through B
    Then the answer's latestGroups is '["platform","Ops"]'

  @op:managedUserGroupManagedUsers @op:managedUser @op:viewer @case:denial
  Scenario: Signing in again with no groups and entering the account again ends the group's membership and its access
    Given the member "nora" is invited to the home account
    And token "h" sends "POST /api/v1/managed-user-groups" with '{"groupName":"platform","roleBindings":[{"roleID":"space-admin","spaceID":"legacy"}]}' through A
    And the answer's id names "platform"
    And the provider "okta" signs in "nora-sub" next with the claims '{"preferred_username":"nora","groups":["platform"]}'
    And the browser signs in through the provider "okta" at A
    And the browser switches to the home account through B
    And the browser sends "GET /api/v1/managed-users/{nora}" through A
    And it answers 200
    Given the provider "okta" signs in "nora-sub" next with the claims '{"preferred_username":"nora","groups":[]}'
    When the browser signs in through the provider "okta" at B
    Then the browser's ended cookie answers 401 at the viewer through A within a second
    When the browser switches to the home account through A
    And the browser sends "GET /api/v1/managed-user-groups/{platform}/managed-users" through B
    Then it answers a 403 problem "forbidden"
    When the browser sends "GET /api/v1/managed-users/{nora}" through A
    Then it answers a 403 problem "forbidden"
    And the browser's read of the space "legacy" through B is not found
    When the browser sends "GET /api/v1/viewer" through A
    Then the answer's teams is '[]'
    When token "h" sends "GET /api/v1/managed-user-groups/{platform}/managed-users" through B
    Then the answer lists the managed users ""

  @op:managedUserGroupManagedUsers @op:managedUser @case:failure
  Scenario: A groups overage and values that are not strings are no groups, a mapped groups claim is read, and an unknown user or group is not found
    Given the member "nora" is invited to the home account
    And token "h" sends "POST /api/v1/managed-user-groups" with '{"groupName":"platform","roleBindings":[]}' through A
    And the answer's id names "platform"
    And the provider "okta" signs in "nora-sub" next with the claims '{"preferred_username":"nora","groups":["platform"]}'
    And the browser signs in through the provider "okta" at A
    And the browser switches to the home account through B
    Given the provider "okta" signs in "nora-sub" next with the fault "groups-overage"
    When the browser signs in through the provider "okta" at A
    And the browser switches to the home account through B
    And token "h" sends "GET /api/v1/managed-users/{nora}" through A
    Then the answer's latestGroups is '[]'
    And the answer's groupCount is '0'
    Given the provider "okta" takes the groups from the claim "cognito:groups"
    And the provider "okta" signs in "nora-sub" next with the claims '{"preferred_username":"nora","cognito:groups":["platform",7,{"name":"ops"}],"groups":["ops"]}'
    When the browser signs in through the provider "okta" at B
    And the browser switches to the home account through A
    And token "h" sends "GET /api/v1/managed-users/{nora}" through B
    Then the answer's latestGroups is '["platform"]'
    And the answer's groupCount is '1'
    When token "h" sends "GET /api/v1/managed-user-groups/{platform}/managed-users" through A
    Then the answer lists the managed users "nora"
    When token "h" sends "GET /api/v1/managed-users/01KAAAAAAAAAAAAAAAAAAAAAAA" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/managed-user-groups/01KAAAAAAAAAAAAAAAAAAAAAAA/managed-users" through A
    Then it answers a 404 problem "not-found"

  @policy-login @op:authSessionAccount @op:viewer @case:success @case:denial
  Scenario: A login policy reads the sign-in's groups as the session's teams
    Given the sibling key is exchanged through B for token "s"
    And token "s" sends "POST /api/v1/policies" with '{"name":"Platform and Ops","type":"LOGIN","body":"package infraweave\nallow if { input.session.member; input.session.teams == [\"platform\", \"Ops\"] }\nroles[\"legacy\"][\"space-reader\"] if { true }\n"}' through B
    And it answers 201
    And token "s" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through A
    And it answers 200
    And the provider "okta" signs in "nora-sub" next with the claims '{"preferred_username":"nora","groups":["platform","Ops"]}'
    When the browser signs in through the provider "okta" at A
    And the browser switches to the sibling account through B
    And the browser sends "GET /api/v1/viewer" through A
    Then the answer's teams is '["platform","Ops"]'
    And the answer's identityProvider is '"OIDC"'
    And the browser's read of the space "legacy" through B allows exactly the role "space-reader"'s actions
    Given the provider "okta" signs in "nora-sub" next with the claims '{"preferred_username":"nora","groups":["Ops"]}'
    When the browser signs in through the provider "okta" at B
    Then switching the browser to the sibling account through A answers a 404 problem "not-found"
