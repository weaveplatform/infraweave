@postgres @vcs
Feature: Runs queue on their stack
  A caller triggers a run on a stack: a tracked run by default, or a
  proposed one, on the commit the stack tracks or on one the caller names.
  The run is created QUEUED, and a second trigger of the same stack, type
  and commit, while the first has not ended, answers the first. A space
  writer triggers runs; a space reader is refused, and a stack the caller
  cannot read, or another account's, answers as one that does not exist.
  A locked stack takes runs from its lock's holder alone; a disabled
  stack, and one that tracks no commit yet, take none. A commit the
  repository lacks is refused at /commitSha. The home key is space-admin
  on the account's top space; the member key holds only the bindings a
  scenario gives it. Stack infra lives in legacy and stack hidden in team.

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy"},"manageState":false}' through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Hidden","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}"},"manageState":false}' through A
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A

  @op:runTrigger @case:success
  Scenario: A trigger queues a tracked run on the tracked commit once, and a proposed run on a named commit
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{}' through A
    Then it answers 200
    And the answer's state is '"QUEUED"'
    And the answer's type is '"TRACKED"'
    And the answer's stackId is '"infra"'
    And the answer's commits is '["{seed}"]'
    And the answer's id names "tracked"
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"TRACKED"}' through B
    Then it answers 200
    And the answer's id is '"{tracked}"'
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through B
    Then it answers 200
    And the answer's type is '"PROPOSED"'
    And the answer's state is '"QUEUED"'
    And the answer's commits is '["{seed}"]'

  @op:runTrigger @case:success
  Scenario: A locked stack takes runs from its lock's holder alone
    Given the member key is space-writer on the space "legacy"
    When token "h" sends "POST /api/v1/stacks/infra/lock" through A
    Then it answers 200
    When token "m" sends "POST /api/v1/stacks/infra/runs" with '{}' through B
    Then it answers a 403 problem "forbidden"
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{}' through A
    Then it answers 200
    And the answer's state is '"QUEUED"'

  @op:runTrigger @case:denial
  Scenario: A space reader is refused, and a stack the caller cannot read does not exist
    Given the member key is space-reader on the space "legacy"
    And the sibling key is exchanged through A for token "sib"
    When token "m" sends "POST /api/v1/stacks/infra/runs" with '{}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_TRIGGER"'
    When token "m" sends "POST /api/v1/stacks/hidden/runs" with '{}' through B
    Then it answers a 404 problem "not-found"
    When token "sib" sends "POST /api/v1/stacks/infra/runs" with '{}' through A
    Then it answers a 404 problem "not-found"
    Given the member key is space-writer on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/runs" with '{}' through B
    Then it answers 200

  @op:runTrigger @case:failure
  Scenario: An unknown commit, a disabled stack, a stack with no commit and an unknown field are refused
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"0123456789abcdef0123456789abcdef01234567"}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"TASK"}' through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"branch":"main"}' through A
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Gone","repository":"gone","branch":"main","provider":"GIT","repositoryURL":"https://127.0.0.1:1/gone.git","space":"legacy"},"manageState":false}' through B
    Then it answers 201
    When token "h" sends "POST /api/v1/stacks/gone/runs" with '{}' through A
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "POST /api/v1/stacks/infra/disable" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{}' through A
    Then it answers a 409 problem "invalid-state"
