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
  the provider the member last signed in through. Under MANAGED_USERS a
  member that is not an account's managed user enters it when one of its
  sign-in's groups names a managed user group of the account that holds a
  binding: the account switcher lists the account, and the first entry
  makes the member a managed user with those groups, taking a seat, or is
  refused with limit-reached when none is left. The managed user outlives
  the group. Each sign-in is a real one through the scenario's in-process
  issuer.
  ref: decision 0006 § "Human login"
  ref: decision 0007 § "Bindings"
  ref: decision 0007 § "Schemes and login"

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

  @op:authAccounts @op:authSessionAccount @op:managedUsers @op:managedUserGroupManagedUsers @op:managedUserGroupDelete @case:success
  Scenario: A member no account admits enters an account whose bound group its sign-in's groups name, and stays its managed user without the group
    Given token "h" sends "POST /api/v1/managed-user-groups" with '{"groupName":"ops","roleBindings":[{"roleID":"space-reader","spaceID":"legacy"}]}' through A
    And the answer's id names "ops"
    And the provider "okta" signs in "nora-sub" next with the claims '{"preferred_username":"nora","groups":["ops"]}'
    When the browser signs in through the provider "okta" at A
    Then the browser's account switcher through B lists "acme"
    When token "h" sends "GET /api/v1/managed-users" through A
    Then the answer lists the managed users ""
    When the browser switches to the home account through B
    And the browser sends "GET /api/v1/viewer" through A
    Then the answer's teams is '["ops"]'
    And the browser's read of the space "legacy" through B allows exactly the role "space-reader"'s actions
    When token "h" sends "GET /api/v1/managed-user-groups/{ops}/managed-users" through A
    Then the answer lists the managed users "nora"
    When token "h" sends "DELETE /api/v1/managed-user-groups/{ops}" through B
    Then the answer's groupName is '"ops"'
    Given the provider "okta" signs in "nora-sub" next with the claims '{"preferred_username":"nora","groups":["ops"]}'
    When the browser signs in through the provider "okta" at A
    Then the browser's account switcher through A lists "acme"
    And the browser switches to the home account through B
    When token "h" sends "GET /api/v1/managed-users" through A
    Then the answer lists the managed users "nora"

  @op:authAccounts @op:authSessionAccount @op:managedUsers @case:denial
  Scenario: A group without a binding, or with another name, admits no one
    Given token "h" sends "POST /api/v1/managed-user-groups" with '{"groupName":"ops","roleBindings":[]}' through A
    And the sibling key is exchanged through B for token "s"
    And token "s" sends "POST /api/v1/managed-user-groups" with '{"groupName":"Ops","roleBindings":[{"roleID":"space-reader","spaceID":"legacy"}]}' through B
    And it answers 201
    And the provider "okta" signs in "nora-sub" next with the claims '{"preferred_username":"nora","groups":["ops"]}'
    When the browser signs in through the provider "okta" at A
    Then the browser's account switcher through B lists no account
    And switching the browser to the home account through A answers a 404 problem "not-found"
    And switching the browser to the sibling account through B answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/managed-users" through A
    Then the answer lists the managed users ""
    When token "s" sends "GET /api/v1/managed-users" through B
    Then the answer lists the managed users ""

  @op:authSessionAccount @op:managedUsers @case:failure
  Scenario: With no seat left, entering through a group is refused with limit-reached and makes no managed user
    Given token "h" sends "POST /api/v1/managed-user-groups" with '{"groupName":"ops","roleBindings":[{"roleID":"space-reader","spaceID":"legacy"}]}' through A
    And it answers 201
    And the provider "okta" signs in "nora-sub" next with the claims '{"preferred_username":"nora","groups":["ops"]}'
    And the browser signs in through the provider "okta" at A
    And the operator key is exchanged through A for token "op"
    And token "op" creates the tier "Gold" with the feature "managed-users" and 1 seats through A
    And token "op" gives the home organization the tier "Gold" through A
    And token "op" grants the home organization the feature "auth" through A
    And token "op" turns billing on through A
    When switching the browser to the home account through B answers a 403 problem "limit-reached"
    Then the problem names the limit "seats" of the organization at 1
    When token "h" sends "GET /api/v1/managed-users" through A
    Then the answer lists the managed users ""
