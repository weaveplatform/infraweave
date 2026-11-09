@postgres
Feature: Stacks
  A stack lives in a space and is named by a slug made from its name when
  it is created, which changes only when it is reslugged. Account admins,
  space-admin on the top space, create, change, move, reslug, disable,
  enable and delete stacks; a space writer locks and unlocks them; a space
  reader reads them and stars them, each star the caller's own. A lock is
  lifted by its holder, or forced by a caller who may force it, and refuses
  every other lock. A stack protected from deletion is not deleted, and a
  space that holds a stack is not either. A stack in a space the caller
  cannot read answers as one that does not exist. Until runs exist, a
  stack's run and task lists are empty pages and a run by ID is not found,
  each read decided on the stack's space. The home key is
  space-admin on the account's top space; the member key holds only the
  bindings a scenario gives it.

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"

  @op:stackCreate @op:stack @op:stackUpdate @op:stacks @op:stackReslug @op:stackDelete @case:success
  Scenario: An account admin creates, reads, changes, moves, reslugs and deletes a stack
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy","labels":["b","a"],"beforePlan":["make"]},"manageState":false}' through A
    Then it answers 201
    And the answer's id is '"infra"'
    And the answer's space is '"legacy"'
    And the answer's labels is '["a","b"]'
    And the answer's state is '"NONE"'
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then it answers 200
    And the answer's hooks is '{"beforeInit":[],"afterInit":[],"beforePlan":["make"],"afterPlan":[],"beforeApply":[],"afterApply":[],"beforeDestroy":[],"afterDestroy":[],"beforePerform":[],"afterPerform":[],"afterRun":[]}'
    And the answer's provider is '"GITHUB"'
    When token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra Two","repository":"infra","branch":"dev","space":"{team}","description":"ours"}' through B
    Then it answers 200
    And the answer's id is '"infra"'
    And the answer's space is '"{team}"'
    And the answer's branch is '"dev"'
    And the answer's labels is '[]'
    When token "h" sends "GET /api/v1/stacks" through A
    Then the answer lists the stacks "infra"
    When token "h" sends "POST /api/v1/stacks/infra/reslug" through A
    Then it answers 200
    And the answer's id is '"infra-two"'
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "DELETE /api/v1/stacks/infra-two" through B
    Then it answers 200
    When token "h" sends "GET /api/v1/stacks/infra-two" through A
    Then it answers a 404 problem "not-found"

  @op:stackCreate @op:stackUpdate @op:stack @op:stacks @op:stackReslug @op:stackDisable @op:stackEnable @op:stackUnlock @op:starSet @case:failure
  Scenario: A stack needs a name, a repository, a branch, a free slug and a space the account holds
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"nowhere"},"manageState":false}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"other","branch":"main","space":"legacy"},"manageState":false}' through B
    Then it answers a 409 problem "already-exists"
    When token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"","space":"legacy"}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "GET /api/v1/stacks?cursor=nope" through B
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "GET /api/v1/stacks/gone" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks/gone/reslug" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks/gone/disable" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks/gone/enable" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks/gone/unlock" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "PUT /api/v1/stacks/gone/star" with '{"star":true}' through B
    Then it answers a 404 problem "not-found"

  @op:stackDelete @op:spaceDelete @case:failure
  Scenario: A protected stack is not deleted, nor is the space that holds it
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"{team}","protectFromDeletion":true},"manageState":false}' through A
    When token "h" sends "DELETE /api/v1/stacks/infra" through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "DELETE /api/v1/spaces/{team}" through A
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "still holds entities"
    When token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"main","space":"{team}"}' through B
    And token "h" sends "DELETE /api/v1/stacks/infra" through A
    Then it answers 200
    When token "h" sends "DELETE /api/v1/spaces/{team}" through B
    Then it answers 200

  @op:stacks @op:stack @op:stackCreate @op:stackUpdate @op:stackDelete @op:stackReslug @op:stackDisable @op:stackEnable @op:stackLock @op:starSet @case:denial
  Scenario: A space reader reads the stacks of its spaces alone and changes none
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Other","repository":"other","branch":"main","space":"{team}"},"manageState":false}' through A
    And the member key is space-reader on the space "legacy"
    When token "m" sends "GET /api/v1/stacks" through B
    Then the answer lists the stacks "infra"
    When token "h" sends "GET /api/v1/stacks" through B
    Then the answer lists the stacks "infra, other"
    When token "m" sends "GET /api/v1/stacks/other" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "PUT /api/v1/stacks/other/star" with '{"star":true}' through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/stacks/infra" through A
    Then it answers 200
    And the answer's canWrite is 'false'
    When token "m" sends "POST /api/v1/stacks" with '{"input":{"name":"Mine","repository":"mine","branch":"main","space":"legacy"},"manageState":false}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_CREATE"'
    When token "m" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"dev","space":"legacy"}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_UPDATE"'
    When token "m" sends "POST /api/v1/stacks/infra/reslug" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_RESLUG"'
    When token "m" sends "POST /api/v1/stacks/infra/disable" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_DISABLE"'
    When token "m" sends "POST /api/v1/stacks/infra/enable" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_ENABLE"'
    When token "m" sends "POST /api/v1/stacks/infra/lock" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_LOCK"'
    When token "m" sends "DELETE /api/v1/stacks/infra" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_DELETE"'

  @op:stackLock @op:stackUnlock @case:success @case:denial @case:failure
  Scenario: A lock is lifted by its holder or forced by an admin, and refuses every other lock
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through A
    And the member key is space-writer on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/lock" with '{"note":"mine"}' through B
    Then it answers 200
    And the answer's lockNote is '"mine"'
    When token "m" sends "POST /api/v1/stacks/infra/unlock" through A
    Then it answers 200
    When token "m" sends "POST /api/v1/stacks/infra/lock" through B
    And token "h" sends "POST /api/v1/stacks/infra/lock" through A
    Then it answers a 409 problem "resource-locked"
    When token "h" sends "POST /api/v1/stacks/infra/unlock" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/lock" through A
    Then it answers 200
    When token "m" sends "POST /api/v1/stacks/infra/lock" through B
    Then it answers a 409 problem "resource-locked"
    When token "m" sends "POST /api/v1/stacks/infra/unlock" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_UNLOCK_FORCE"'

  @op:starSet @op:stackDisable @op:stackEnable @op:stack @case:success
  Scenario: Each caller's stars are its own, and an admin disables and enables a stack
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through A
    And the member key is space-reader on the space "legacy"
    When token "m" sends "PUT /api/v1/stacks/infra/star" with '{"star":true}' through B
    Then it answers 200
    And the answer's starred is 'true'
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then the answer's starred is 'false'
    When token "m" sends "PUT /api/v1/stacks/infra/star" with '{"star":false}' through A
    Then the answer's starred is 'false'
    When token "h" sends "POST /api/v1/stacks/infra/disable" through B
    Then it answers 200
    And the answer's isDisabled is 'true'
    When token "h" sends "POST /api/v1/stacks/infra/enable" through A
    Then the answer's isDisabled is 'false'

  @op:stackCreate @op:stackPatch @case:failure @case:success
  Scenario: A stack's labels are 1 to 255 characters with no control characters or edge whitespace, at most 100, each once
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy","labels":["{256 × x}","a\u0007b"," edge","ok","ok"]},"manageState":false}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/input/labels/0" as "max-length"
    And the problem refuses the field "/input/labels/1" as "pattern"
    And the problem refuses the field "/input/labels/2" as "pattern"
    And the problem refuses the field "/input/labels/4" as "duplicate"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy","labels":[{101 labels}]},"manageState":false}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/input/labels" as "max-length"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy","labels":["prod","Prod","{255 × x}"]},"manageState":false}' through A
    Then it answers 201
    When token "h" sends "PATCH /api/v1/stacks/infra" with '{"labels":["prod","Prod"]}' through B
    Then it answers 200
    And the answer's labels is '["Prod","prod"]'
    When token "h" sends "PATCH /api/v1/stacks/infra" with '{"labels":["Prod","Prod"]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/labels/1" as "duplicate"

  @op:stackRuns @op:stackRunsInRange @op:stackProposedRuns @op:stackTasks @op:stackRun @case:success @case:failure
  Scenario: A stack's runs and tasks are empty pages until runs exist, and a run by ID is not found
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through A
    And the member key is space-reader on the space "legacy"
    When token "m" sends "GET /api/v1/stacks/infra/runs" through A
    Then it answers 200
    And the answer's items is '[]'
    When token "m" sends "GET /api/v1/stacks/infra/runs-in-range?start=1700000000&end=1700691200" through B
    Then it answers 200
    And the answer's items is '[]'
    When token "m" sends "GET /api/v1/stacks/infra/proposed-runs?limit=5" through A
    Then it answers 200
    And the answer's items is '[]'
    When token "h" sends "GET /api/v1/stacks/infra/tasks" through B
    Then it answers 200
    And the answer's items is '[]'
    When token "m" sends "GET /api/v1/stacks/infra/run?id=01JBQ6Q4V2ZK5Y3W8N7M6P5R4T" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/stacks/infra/runs-in-range?start=yesterday&end=1700691200" through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "#/query/start" as "format"
    When token "m" sends "GET /api/v1/stacks/infra/runs-in-range?start=1700000000&end=1700691201" through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "#/query/end" as "rule"

  @op:stackRuns @op:stackRunsInRange @op:stackProposedRuns @op:stackTasks @op:stackRun @case:denial
  Scenario: The runs and tasks of a stack the caller cannot read answer as a stack that does not exist
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Other","repository":"other","branch":"main","space":"{team}"},"manageState":false}' through A
    And the member key is space-reader on the space "legacy"
    When token "m" sends "GET /api/v1/stacks/other/runs" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/stacks/other/runs-in-range?start=1700000000&end=1700000001" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/stacks/other/proposed-runs" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/stacks/other/tasks" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/stacks/other/run?id=01JBQ6Q4V2ZK5Y3W8N7M6P5R4T" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/stacks/missing/runs" through B
    Then it answers a 404 problem "not-found"

  @op:stackRuns @op:stackRunsInRange @op:stackProposedRuns @op:stackTasks @case:failure
  Scenario: An altered cursor on a stack's runs or tasks answers 400
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through A
    When token "h" sends "GET /api/v1/stacks/infra/runs?cursor=nope" through A
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "GET /api/v1/stacks/infra/runs-in-range?start=1700000000&end=1700000001&cursor=nope" through B
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "GET /api/v1/stacks/infra/proposed-runs?cursor=nope" through A
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "GET /api/v1/stacks/infra/tasks?cursor=nope" through B
    Then it answers a 400 problem "invalid-cursor"
