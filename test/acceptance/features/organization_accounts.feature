@postgres
Feature: Organization accounts, members and keys
  The Organization tag serves an organization admin: a member holding the
  organization role organization-admin, the break-glass admin, or an
  organization key's token. It creates, claims, renames and deletes the
  organization's accounts, deactivates members, grants and revokes the role,
  and manages organization keys, which exchange at /api/v1/auth/token for a
  token only this tag accepts. No answer of the tag names an account's
  stacks, runs, state or secrets.
  ref: decision 0007

  Background:
    Given two infraweave servers, A and B, on one database, with an operator key
    And the password "correct horse" hashed by infraweave auth hash-password
    And the operator key is exchanged through A for token "op"
    And token "op" creates the organization "initech" with the account "main" and the break-glass login "admin" through A
    And the organization "initech" has the active members "alice, bob"
    And the browser signs in to "initech" as "admin" with the password "correct horse" through A

  @op:organizationAccountClaim @op:organizationAccounts @case:success @case:failure
  Scenario: The break-glass admin claims the first account once
    When the browser sends "POST /api/v1/organization/accounts/main/claim" with '{"admin":"{alice}"}' through B
    Then it answers 200
    And the answer's claimed is 'true'
    When the browser sends "GET /api/v1/organization/accounts" through A
    Then the answer lists the accounts "main:claimed"
    When the browser sends "POST /api/v1/organization/accounts/main/claim" with '{"admin":"{bob}"}' through A
    Then it answers a 409 problem "invalid-state"
    And no answer of the Organization tag names an account's stack, run, state or secret

  @op:organizationAccountCreate @op:organizationAccounts @op:organizationAccountUpdate @op:organizationAccountDelete @case:success @case:failure
  Scenario: An organization admin creates, lists, renames and deletes accounts, but not the last
    When the browser sends "POST /api/v1/organization/accounts" with '{"slug":"initech-ops","name":"Ops","admin":"{bob}"}' through B
    Then it answers 201
    When the browser sends "GET /api/v1/organization/accounts" through A
    Then the answer lists the accounts "main:unclaimed, initech-ops:claimed"
    When the browser sends "POST /api/v1/organization/accounts" with '{"slug":"initech-ops","name":"Ops again","admin":"{bob}"}' through A
    Then it answers a 409 problem "already-exists"
    When the browser sends "PATCH /api/v1/organization/accounts/initech-ops" with '{"name":"Operations"}' through B
    Then the answer's name is '"Operations"'
    When the browser sends "DELETE /api/v1/organization/accounts/initech-ops" through A
    Then it answers 200
    When the browser sends "GET /api/v1/organization/accounts" through B
    Then the answer lists the accounts "main:unclaimed"
    When the browser sends "DELETE /api/v1/organization/accounts/main" through A
    Then it answers a 409 problem "invalid-state"
    When the browser sends "DELETE /api/v1/organization/accounts/no-such" through B
    Then it answers a 404 problem "not-found"
    And no answer of the Organization tag names an account's stack, run, state or secret

  @op:organization @op:organizationUpdate @op:organizationMembers @op:organizationAdminGrant @op:organizationAdminRevoke @op:organizationMemberDeactivate @op:organizationLimits @case:success @case:failure
  Scenario: An organization admin renames the organization and manages its members
    When the browser sends "PATCH /api/v1/organization" with '{"name":"Initech Corp"}' through A
    And the browser sends "GET /api/v1/organization" through B
    Then the answer's name is '"Initech Corp"'
    When the browser sends "GET /api/v1/organization/members" through A
    Then it answers 200
    When the browser sends "PUT /api/v1/organization/members/{alice}/organization-admin" through B
    Then the answer's organizationAdmin is 'true'
    When the browser sends "DELETE /api/v1/organization/members/{alice}/organization-admin" through A
    Then the answer's organizationAdmin is 'false'
    When the browser sends "POST /api/v1/organization/members/{bob}/deactivate" through B
    Then the answer's status is '"DEACTIVATED"'
    When the browser sends "POST /api/v1/organization/accounts/main/claim" with '{"admin":"{bob}"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "admin"
    When the browser sends "GET /api/v1/organization/limits" through B
    Then it answers 200
    And no answer of the Organization tag names an account's stack, run, state or secret

  @op:organizationKeyCreate @op:organizationKeys @op:organizationAccounts @op:organizationKeyDisable @op:organizationKeyDelete @case:success @case:denial
  Scenario: An organization key's token reaches the tag and nothing else
    When the browser creates the organization key "ci" through A
    And the organization key "ci" exchanges through B for token "org"
    And token "org" sends "GET /api/v1/organization/accounts" through A
    Then the answer lists the accounts "main:unclaimed"
    When token "org" sends "GET /api/v1/organization/keys" through B
    Then it answers 200
    When token "org" sends "GET /api/v1/viewer" through A
    Then it answers a 401 problem "unauthenticated"
    When the browser sends "POST /api/v1/organization/keys/{ci}/disable" through B
    Then the answer's disabled is 'true'
    And exchanging the organization key "ci" through A answers a 401 problem "unauthenticated"
    When the browser sends "DELETE /api/v1/organization/keys/{ci}" through A
    Then it answers 200
    And no answer of the Organization tag names an account's stack, run, state or secret

  @op:organizationAccounts @op:organizationUsage @case:denial
  Scenario: An account API key's token is refused on the tag
    When the home key is exchanged through B for token "h"
    And token "h" sends "GET /api/v1/organization/accounts" through A
    Then it answers a 401 problem "unauthenticated"
    When token "h" sends "GET /api/v1/organization/usage" through B
    Then it answers a 401 problem "unauthenticated"
