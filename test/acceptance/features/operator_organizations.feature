@postgres
Feature: Operator organizations
  The operator API under /operator/v1 creates the installation's
  organizations, each with its first account, unclaimed, and sets each
  organization's break-glass credential, whose password hash infraweave auth
  hash-password prints. An operator key in auth.operator.keys exchanges at
  /api/v1/auth/token for an operator token, which only the operator API
  accepts; the operator API accepts nothing else. Every write is on the
  organization's trail with actor kind operator. Deleting an organization
  answers 404 for it at once, and the other domains' purges then remove the
  rows they keep of it and its accounts.
  ref: decisions 0006 and 0021

  @case:success
  Scenario: An operator key creates, lists, reads and deletes an organization
    Given two infraweave servers, A and B, on one database, with an operator key
    And the password "correct horse" hashed by infraweave auth hash-password
    Then the hash verifies "correct horse" and not "wrong horse"
    When the operator key is exchanged through A for token "op"
    And token "op" creates the organization "initech" with the account "main" and the break-glass login "admin" through A
    Then the organizations listed through B with token "op" include "initech"
    And the organization "initech" read through B with token "op" has the account "main", unclaimed, and the break-glass login "admin"
    When token "op" sets the break-glass login of "initech" to "root-admin" through B
    Then the organization "initech" read through A with token "op" has the account "main", unclaimed, and the break-glass login "root-admin"
    When token "op" removes the break-glass credential of "initech" through A
    Then the organization "initech" read through B with token "op" has the account "main", unclaimed, and no break-glass credential
    And the account "main" holds a stack and an account limit
    When token "op" deletes the organization "initech" through B
    Then "GET /operator/v1/organizations/initech" through A with token "op" answers a 404 problem "not-found"
    And no stack, limit or entitlement of the account "main" or its organization remains
    And the organizations listed through A with token "op" leave out "initech"
    And the trail of "initech" holds "organization.create", "organization.break_glass_set", "organization.break_glass_delete" and "organization.delete" by the operator

  @case:failure
  Scenario: A slug in use and a weak hash are refused
    Given two infraweave servers, A and B, on one database, with an operator key
    And the password "correct horse" hashed by infraweave auth hash-password
    When the operator key is exchanged through A for token "op"
    And token "op" creates the organization "initech" with the account "main" and the break-glass login "admin" through A
    Then creating the organization "initech" again through B with token "op" answers a 409 problem "already-exists"
    And creating the organization "weak" with a hash of 599999 iterations through A with token "op" answers a 422 problem "validation-failed" naming "breakGlass.passwordHash"
    And the organizations listed through A with token "op" leave out "weak"

  @case:denial
  Scenario: Each API accepts only its own tokens
    Given two infraweave servers, A and B, on one database, with an operator key
    When the operator key is exchanged through A for token "op"
    And the home key is exchanged through B for token "h"
    Then "GET /operator/v1/organizations" through A with token "h" answers a 401 problem "unauthenticated"
    And "GET /operator/v1/organizations" without a credential answers a 401 problem "unauthenticated"
    And "GET /api/v1/viewer" through B with token "op" answers a 401 problem "unauthenticated"
    And token "op" carries the claims of an operator token
