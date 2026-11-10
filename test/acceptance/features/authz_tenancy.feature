@postgres
Feature: Spaces and their stacks stay within their account
  Each account has its own tree of spaces, and a space of another account
  answers as one that does not exist, whether that account is of the same
  organization or another. A binding grants only in its own account. A new
  account starts with its top space, named by its slug, legacy below it,
  its system roles and its first admin, space-admin on its top space. The
  home and sibling accounts are of one organization, and the stranger
  account of another; each key is space-admin on its account's top space.
  A stack, in its account's space, is the same: another account's answers
  as one that does not exist, and a slug is taken only within its account.

  Background:
    Given two infraweave servers, A and B, on one database, with an operator key
    And the home key is exchanged through A for token "h"
    And the sibling key is exchanged through B for token "s"
    And the stranger key is exchanged through A for token "x"

  @op:spaces @case:success
  Scenario: Each account sees only its own spaces
    When token "h" sends "GET /api/v1/spaces" through A
    Then the answer lists the spaces "acme, legacy"
    When token "s" sends "GET /api/v1/spaces" through B
    Then the answer lists the spaces "acme-dev, legacy"
    When token "x" sends "GET /api/v1/spaces" through A
    Then the answer lists the spaces "globex, legacy"
    When token "s" sends "GET /api/v1/spaces/legacy" through A
    Then the answer's parentSpace is '"acme-dev"'

  @op:space @op:spaceUpdate @op:spaceDelete @op:spaceCreate @case:denial
  Scenario: Another account's space answers 404 within the organization and across it
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    When token "s" sends "GET /api/v1/spaces/{team}" through B
    Then it answers a 404 problem "not-found"
    When token "x" sends "GET /api/v1/spaces/{team}" through A
    Then it answers a 404 problem "not-found"
    When token "s" sends "PUT /api/v1/spaces/{team}" with '{"name":"Mine","description":"","inheritEntities":false,"parentSpace":"acme-dev"}' through A
    Then it answers a 404 problem "not-found"
    When token "x" sends "DELETE /api/v1/spaces/{team}" through B
    Then it answers a 404 problem "not-found"
    When token "s" sends "POST /api/v1/spaces" with '{"name":"Below","description":"","inheritEntities":false,"parentSpace":"{team}"}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/spaces/{team}" through B
    Then it answers 200

  @op:space @op:spaceCreate @op:spaceDelete @case:denial
  Scenario: A space-admin binding on one account's top space grants nothing in another
    When token "h" sends "GET /api/v1/spaces/acme-dev" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/spaces/globex" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/spaces" with '{"name":"Over","description":"","inheritEntities":false,"parentSpace":"acme-dev"}' through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "DELETE /api/v1/spaces/globex" through B
    Then it answers a 404 problem "not-found"

  @op:stacks @op:stack @op:stackCreate @op:stackLock @op:stackDelete @op:searchStacks @op:searchStacksSuggestions @case:denial
  Scenario: Another account's stack answers 404, and each account's slugs are its own
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy","labels":["prod"]},"manageState":false}' through A
    Then token "s" searching the stacks through B in pages of 1 finds "" forwards and back
    And token "x" searching the stacks through A in pages of 1 finds "" forwards and back
    When token "s" sends "POST /api/v1/stacks/search/suggestions" with '{"fields":["label"]}' through A
    Then the answer suggests the label values "" of 0 stacks
    When token "x" sends "POST /api/v1/stacks/search/suggestions" with '{"fields":["label"]}' through B
    Then the answer suggests the label values "" of 0 stacks
    When token "s" sends "GET /api/v1/stacks/infra" through B
    Then it answers a 404 problem "not-found"
    When token "x" sends "POST /api/v1/stacks/infra/lock" through A
    Then it answers a 404 problem "not-found"
    When token "x" sends "GET /api/v1/stacks" through B
    Then the answer lists the stacks ""
    When token "s" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through B
    Then it answers 201
    When token "s" sends "DELETE /api/v1/stacks/infra" through A
    Then it answers 200
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then it answers 200
    When token "s" sends "POST /api/v1/stacks" with '{"input":{"name":"Over","repository":"infra","branch":"main","space":"acme"},"manageState":false}' through A
    Then it answers a 404 problem "not-found"

  @op:organizationAccountCreate @case:success
  Scenario: A new account starts with its top space, legacy, its system roles and its first admin
    Given the password "correct horse" hashed by infraweave auth hash-password
    And the operator key is exchanged through A for token "op"
    And token "op" creates the organization "initech" with the account "main" and the break-glass login "admin" through A
    And the organization "initech" has the active members "alice, bob"
    And the browser signs in to "initech" as "admin" with the password "correct horse" through A
    When the browser sends "POST /api/v1/organization/accounts" with '{"slug":"initech-ops","name":"Ops","admin":"{bob}"}' through B
    Then it answers 201
    And the account "initech-ops" has its top space, legacy below it, the system roles and "bob" as space-admin on its top space

  @op:contexts @op:context @op:contextAttach @op:searchContexts @op:attachableContextsInSpace @case:denial
  Scenario: Each account attaches only its own contexts, and another account's answers 404
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through A
    And token "h" sends "POST /api/v1/contexts" with '{"name":"Shared","space":"legacy","stackAttachments":[{"stack":"infra","priority":0}]}' through B
    And token "s" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through B
    When token "s" sends "GET /api/v1/contexts/shared" through A
    Then it answers a 404 problem "not-found"
    When token "x" sends "GET /api/v1/contexts/shared" through B
    Then it answers a 404 problem "not-found"
    When token "s" sends "PUT /api/v1/stacks/infra/contexts/shared" with '{"priority":0}' through A
    Then it answers a 404 problem "not-found"
    When token "s" sends "GET /api/v1/contexts" through B
    Then the answer lists the contexts ""
    When token "x" sends "POST /api/v1/contexts/search" with '{}' through A
    Then the answer lists the contexts ""
    When token "s" sends "GET /api/v1/attachable-contexts-in-space?id=legacy" through B
    Then the answer lists the contexts ""
    When token "s" sends "POST /api/v1/contexts" with '{"name":"Shared","space":"legacy","stackAttachments":[{"stack":"infra","priority":0}]}' through A
    Then it answers 201
    And the stack "infra" read through B with token "s" eventually attaches the contexts "shared"
    And the stack "infra" read through A with token "h" eventually attaches the contexts "shared"
    When token "s" sends "DELETE /api/v1/contexts/shared?force=true" through B
    Then it answers 200
    And the stack "infra" read through A with token "h" eventually attaches the contexts "shared"
    And the stack "infra" read through B with token "s" eventually attaches the contexts ""
