@postgres
Feature: Spaces
  An account's spaces form a tree under its top space, whose slug is the
  account's. Account admins, space-admin on the top space, create, change,
  move and delete spaces anywhere. An admin of a space below the top
  changes only the name, description and labels of the spaces they
  administer while the account's allowNonAdminSpaceCreation is off; while
  it is on they also create spaces below them, which always inherit, move
  them between spaces they administer and delete them. The top space and
  legacy are never deleted, a space with child spaces is not, and no space
  moves below itself. Each space answers the caller's access level and the
  actions the caller may perform there. Lists, searches and suggestions
  hold only the spaces the caller may read, so every page before the last
  is full and no count names another. The home key is space-admin on
  the account's top space; the member key holds only the bindings a
  scenario gives it.

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"

  @op:spaceCreate @op:space @op:spaceUpdate @op:spaceDelete @op:spaces @case:success
  Scenario: An account admin creates, renames, relabels, moves and deletes spaces
    When token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":false,"labels":["a"],"parentSpace":"acme"}' through A
    Then it answers 201
    And the answer's id is the space "team"
    And the answer's parentSpace is '"acme"'
    And the answer's accessLevel is '"ADMIN"'
    When token "h" sends "POST /api/v1/spaces" with '{"name":"Child","description":"","inheritEntities":true,"parentSpace":"{team}"}' through B
    Then it answers 201
    And the answer's id is the space "child"
    When token "h" sends "PUT /api/v1/spaces/{team}" with '{"name":"Team A","description":"ours","inheritEntities":false,"labels":["b","c"],"parentSpace":"acme"}' through B
    Then the answer's name is '"Team A"'
    And the answer's labels is '["b","c"]'
    When token "h" sends "PUT /api/v1/spaces/{child}" with '{"name":"Child","description":"","inheritEntities":true,"parentSpace":"legacy"}' through A
    Then the answer's parentSpace is '"legacy"'
    When token "h" sends "GET /api/v1/spaces/{team}" through A
    Then the answer's description is '"ours"'
    When token "h" sends "GET /api/v1/spaces" through B
    Then the answer lists the spaces "acme, {child}, legacy, {team}"
    When token "h" sends "DELETE /api/v1/spaces/{child}" through A
    Then it answers 200
    When token "h" sends "GET /api/v1/spaces/{child}" through B
    Then it answers a 404 problem "not-found"

  @op:spaceUpdate @op:spaceDelete @case:failure
  Scenario: The tree refuses a move below itself and the deletes it cannot take, naming why
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Child","description":"","inheritEntities":false,"parentSpace":"{team}"}' through A
    And the answer's id is the space "child"
    When token "h" sends "PUT /api/v1/spaces/{team}" with '{"name":"Team","description":"","inheritEntities":false,"parentSpace":"{child}"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "which is inside it"
    When token "h" sends "DELETE /api/v1/spaces/{team}" through A
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "has child spaces"
    When token "h" sends "DELETE /api/v1/spaces/acme" through B
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "the top space acme cannot be deleted"
    When token "h" sends "DELETE /api/v1/spaces/legacy" through A
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "the legacy space cannot be deleted"
    When token "h" sends "PUT /api/v1/spaces/acme" with '{"name":"acme","description":"","inheritEntities":false,"parentSpace":"legacy"}' through B
    Then it answers a 422 problem "validation-failed"

  @op:spaceCreate @op:space @case:failure
  Scenario: A space needs a name and a parent the account holds
    When token "h" sends "POST /api/v1/spaces" with '{"name":"","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "POST /api/v1/spaces" with '{"name":"Lost","description":"","inheritEntities":false,"parentSpace":"nowhere"}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/spaces/nowhere" through A
    Then it answers a 404 problem "not-found"

  @op:spaceUpdate @op:spaceCreate @op:spaceDelete @op:space @case:denial
  Scenario: With the setting off, a space admin below the top only renames and relabels their spaces
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And the member key is space-admin on the space "{team}"
    When token "m" sends "PUT /api/v1/spaces/{team}" with '{"name":"Ours","description":"mine","inheritEntities":false,"labels":["x"],"parentSpace":"acme"}' through B
    Then it answers 200
    And the answer's name is '"Ours"'
    When token "m" sends "PUT /api/v1/spaces/{team}" with '{"name":"Ours","description":"mine","inheritEntities":true,"parentSpace":"acme"}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_ADMIN"'
    When token "m" sends "PUT /api/v1/spaces/{team}" with '{"name":"Ours","description":"mine","inheritEntities":false,"parentSpace":"legacy"}' through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/spaces" with '{"name":"Sub","description":"","inheritEntities":true,"parentSpace":"{team}"}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_ADMIN"'
    When token "m" sends "DELETE /api/v1/spaces/{team}" through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "GET /api/v1/spaces/acme" through A
    Then it answers a 404 problem "not-found"

  @op:allowNonAdminSpaceCreation @op:accountToggleAllowNonAdminSpaceCreation @op:spaceCreate @op:spaceDelete @case:success @case:denial @case:failure
  Scenario: With the setting on, a space admin below the top creates inheriting spaces below theirs and deletes them
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And the member key is space-admin on the space "{team}"
    And "GET /api/v1/allow-non-admin-space-creation" through B with token "h" answers 'false'
    When token "m" sends "POST /api/v1/account/toggle-allow-non-admin-space-creation" through A
    Then it answers a 403 problem "forbidden"
    When "POST /api/v1/account/toggle-allow-non-admin-space-creation" through A with token "h" answers 'true'
    Then "GET /api/v1/allow-non-admin-space-creation" through B with token "m" answers 'true'
    When token "m" sends "POST /api/v1/spaces" with '{"name":"Sub","description":"","inheritEntities":false,"parentSpace":"{team}"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "inherits its parent's entities"
    When token "m" sends "POST /api/v1/spaces" with '{"name":"Sub","description":"","inheritEntities":true,"parentSpace":"{team}"}' through A
    Then it answers 201
    And the answer's id is the space "sub"
    And the answer's inheritEntities is 'true'
    When token "m" sends "POST /api/v1/spaces" with '{"name":"Elsewhere","description":"","inheritEntities":true,"parentSpace":"legacy"}' through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "DELETE /api/v1/spaces/{sub}" through B
    Then it answers 200

  @op:spaces @op:space @op:searchSpaces @op:searchSpacesSuggestions @case:denial
  Scenario: A member with no binding sees no space
    When token "m" sends "GET /api/v1/spaces" through A
    Then the answer lists the spaces ""
    When token "m" sends "GET /api/v1/spaces/legacy" through B
    Then it answers a 404 problem "not-found"
    And token "m" searching the spaces through A in pages of 2 finds "", every page full but the last
    When token "m" sends "POST /api/v1/spaces/search/suggestions" with '{}' through B
    Then the answer suggests the ids "" of 0 spaces

  @op:searchSpaces @op:spaces @case:success
  Scenario: A member reaching three of five spaces searches and lists exactly those, page by page
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Sub","description":"","inheritEntities":false,"parentSpace":"{team}"}' through A
    And the answer's id is the space "sub"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Other","description":"","inheritEntities":false,"parentSpace":"acme"}' through B
    And the answer's id is the space "other"
    And the member key is space-reader on the space "{team}"
    And the member key is space-reader on the space "legacy"
    Then token "m" searching the spaces through A in pages of 2 finds "legacy, {sub}, {team}", every page full but the last
    And token "h" searching the spaces through B in pages of 2 finds "acme, legacy, {other}, {sub}, {team}", every page full but the last
    When token "m" sends "GET /api/v1/spaces?limit=2" through B
    Then the answer lists the spaces "legacy, {sub}"

  @op:searchSpacesSuggestions @case:success
  Scenario: Suggestions list and count only the spaces the caller may read
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And the member key is space-reader on the space "{team}"
    When token "m" sends "POST /api/v1/spaces/search/suggestions" with '{"fields":["id"]}' through B
    Then the answer suggests the ids "{team}" of 1 space
    When token "h" sends "POST /api/v1/spaces/search/suggestions" with '{"fields":["id"],"predicates":[{"field":"parentSpace","constraint":{"stringEquals":["acme"]}}]}' through A
    Then the answer suggests the ids "legacy, {team}" of 2 spaces

  @op:searchSpaces @op:searchSpacesSuggestions @case:failure
  Scenario: A search of a field spaces lack, or with a constraint its field does not take, is refused naming it
    When token "h" sends "POST /api/v1/spaces/search" with '{"predicates":[{"field":"owner","constraint":{"stringEquals":["x"]}}]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/predicates/0/field" as "unknown-field"
    When token "h" sends "POST /api/v1/spaces/search/suggestions" with '{"predicates":[{"field":"inheritEntities","constraint":{"stringEquals":["x"]}}]}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/predicates/0/constraint/stringEquals" as "rule"
    When token "h" sends "POST /api/v1/spaces/search" with '{"after":"not-a-cursor"}' through A
    Then it answers a 400 problem "invalid-cursor"

  @op:actionCatalogue @case:success @case:denial
  Scenario: Any authenticated caller reads the action catalogue with its flags
    When token "m" sends "GET /api/v1/actions" through A
    Then the answer lists 165 actions, "UPDATE_BLUEPRINT_DEPLOYMENT" deprecated and "STACK_CONTEXT_ATTACH" disabled
    When a caller with no token sends "GET /api/v1/actions" through B
    Then it answers a 401 problem "unauthenticated"

  @op:space @op:spaces @case:success
  Scenario: Each space answers the access its resident, propagated and inherited grants give
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Sub","description":"","inheritEntities":true,"parentSpace":"{team}"}' through A
    And the answer's id is the space "sub"
    And the member key is space-writer on the space "{team}"
    When token "m" sends "GET /api/v1/spaces/{team}" through B
    Then the answer's accessLevel is '"WRITE"'
    When token "m" sends "GET /api/v1/spaces/{sub}" through A
    Then the answer's accessLevel is '"WRITE"'
    When token "m" sends "GET /api/v1/spaces/acme" through B
    Then the answer's accessLevel is '"READ"'
    When token "m" sends "GET /api/v1/spaces" through A
    Then the answer lists the spaces "acme, {sub}, {team}"
    And token "m"'s read of the space "{team}" through B allows what the viewer lists for it
    And token "m"'s read of the space "{sub}" through A allows what the viewer lists for it
    And token "m"'s read of the space "acme" through B allows what the viewer lists for it

  @op:spaceCreate @op:spacePatch @case:failure @case:success
  Scenario: A space's labels are 1 to 255 characters with no control characters or edge whitespace, at most 100, each once
    When token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":false,"parentSpace":"acme","labels":["{256 × x}","a\u0007b","edge ","ok","ok"]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/labels/0" as "max-length"
    And the problem refuses the field "/labels/1" as "pattern"
    And the problem refuses the field "/labels/2" as "pattern"
    And the problem refuses the field "/labels/4" as "duplicate"
    When token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":false,"parentSpace":"acme","labels":[{101 labels}]}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/labels" as "max-length"
    When token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":false,"parentSpace":"acme","labels":["prod","Prod","{255 × x}"]}' through A
    Then it answers 201
    And the answer's id is the space "team"
    When token "h" sends "PATCH /api/v1/spaces/{team}" with '{"labels":["prod","Prod"]}' through B
    Then it answers 200
    And the answer's labels is '["prod","Prod"]'
    When token "h" sends "PATCH /api/v1/spaces/{team}" with '{"labels":["Prod","Prod"]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/labels/1" as "duplicate"

  @op:spacePatch @case:denial
  Scenario: A member who reads a space but does not administer it patches nothing, and one it cannot read is not found
    Given the member key is space-reader on the space "legacy"
    When token "m" sends "PATCH /api/v1/spaces/legacy" with '{"description":"mine"}' through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PATCH /api/v1/spaces/acme" with '{"description":"mine"}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/spaces/legacy" through A
    Then the answer's description is '""'
