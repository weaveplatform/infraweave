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

  The scheduler moves a queued run to READY: a blocking run once it takes
  its stack's lock, so a second waits until the first ends, and a proposed
  run whatever the lock. Canceling the run that holds the lock releases
  it, which needs RUN_CANCEL_BLOCKING as well as RUN_CANCEL. A run's
  allowedActions are the actions its state allows next that the caller
  holds, none once it ends; its priority is set until it ends. A run, its
  stack and a stack's lists of runs, newest first and paged by cursor,
  read as their stack does; a search of runs keeps to the stacks the
  caller may read, pages by cursor or by offset, and counts its total.

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

  @op:runCancel @op:run @case:success
  Scenario: A blocking run takes the lock, a second waits, and canceling the first releases it
    Given a commit to "main" of "infra" changing "main.tf"
    And the head of "main" of "infra" is kept as "pushed"
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{}' through A
    And the answer's id names "first"
    And token "h" eventually reads the run "first" in the state "READY" through B
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{pushed}"}' through B
    And the answer's id names "second"
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED"}' through A
    And the answer's id names "proposal"
    Then token "h" eventually reads the run "proposal" in the state "READY" through A
    When token "h" sends "GET /api/v1/runs/{second}" through B
    Then it answers 200
    And the answer's state is '"QUEUED"'
    And the answer's allowedActions is '["RUN_DISCARD","RUN_CANCEL","RUN_PRIORITIZE_SET"]'
    When token "h" sends "GET /api/v1/runs/{first}" through A
    Then the answer's allowedActions is '["RUN_DISCARD","RUN_CANCEL_BLOCKING","RUN_PRIORITIZE_SET"]'
    When token "h" sends "POST /api/v1/stacks/infra/runs/{first}/cancel" with '{"note":"Not now"}' through B
    Then it answers 200
    And the answer's state is '"CANCELED"'
    And the answer's allowedActions is '[]'
    And token "h" eventually reads the run "second" in the state "READY" through A

  @op:runPrioritySet @op:runStack @op:run @op:stackRuns @op:stackProposedRuns @op:stackRun @case:success
  Scenario: A run's priority is read back, its stack answers, and a stack's lists page newest first
    Given a commit to "main" of "infra" changing "main.tf"
    And the head of "main" of "infra" is kept as "pushed"
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{}' through A
    And the answer's id names "first"
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{pushed}"}' through B
    And the answer's id names "second"
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED"}' through A
    And the answer's id names "proposal"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{second}/priority-set" with '{"preset":"high"}' through B
    Then it answers 200
    And the answer's priorityPreset is '"high"'
    When token "h" sends "GET /api/v1/runs/{second}" through A
    Then the answer's priorityPreset is '"high"'
    And the answer's stackId is '"infra"'
    When token "h" sends "GET /api/v1/runs/{proposal}/stack" through B
    Then it answers 200
    And the answer's id is '"infra"'
    When token "h" sends "GET /api/v1/stacks/infra/runs?limit=1" through A
    Then the answer's items are the runs "second"
    And the answer's nextCursor is kept as "page"
    When token "h" sends "GET /api/v1/stacks/infra/runs?limit=1&cursor={page}" through B
    Then the answer's items are the runs "first"
    When token "h" sends "GET /api/v1/stacks/infra/runs?before={second}" through A
    Then the answer's items are the runs "first"
    When token "h" sends "GET /api/v1/stacks/infra/proposed-runs" through B
    Then the answer's items are the runs "proposal"
    When token "h" sends "GET /api/v1/stacks/infra/run?id={first}" through A
    Then it answers 200
    And the answer's type is '"TRACKED"'

  @op:searchRuns @op:searchRunsSuggestions @case:success
  Scenario: A search finds the runs newest first, by a filter and from an offset, with its total and suggestions
    Given a commit to "main" of "infra" changing "main.tf"
    And the head of "main" of "infra" is kept as "pushed"
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{}' through A
    And the answer's id names "first"
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{pushed}"}' through B
    And the answer's id names "second"
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED"}' through A
    And the answer's id names "proposal"
    When token "h" sends "POST /api/v1/runs/search" with '{"input":{}}' through B
    Then it answers 200
    And the answer's edges are the runs "proposal, second, first"
    And the answer's total is '3'
    When token "h" sends "POST /api/v1/runs/search" with '{"input":{"first":1,"predicates":[{"field":"type","constraint":{"enumEquals":["TRACKED"]}}]},"offset":1}' through A
    Then the answer's edges are the runs "first"
    And the answer's total is '2'
    When token "h" sends "POST /api/v1/runs/search" with '{"input":{"fullTextSearch":"{pushed}"}}' through B
    Then the answer's edges are the runs "second"
    When token "h" sends "POST /api/v1/runs/search/suggestions" with '{"fields":["type"]}' through A
    Then it answers 200
    And the answer's filteredCount is '3'

  @op:run @op:runStack @op:searchRuns @op:searchRunsSuggestions @op:runCancel @op:runPrioritySet @case:denial
  Scenario: A run the caller cannot read does not exist, and a reader neither cancels a blocking run nor sets a priority
    Given token "h" sends "POST /api/v1/stacks/infra/runs" with '{}' through A
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through B
    And token "h" sends "POST /api/v1/stacks/hidden/runs" with '{}' through B
    And the answer's id names "secret"
    When token "m" sends "GET /api/v1/runs/{run}" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/runs/{run}/stack" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/runs/search" with '{"input":{}}' through A
    Then the answer's edges are the runs ""
    And the answer's total is '0'
    When token "m" sends "POST /api/v1/runs/search/suggestions" with '{}' through B
    Then the answer's filteredCount is '0'
    Given the member key is space-reader on the space "legacy"
    When token "m" sends "GET /api/v1/runs/{secret}" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/runs/search" with '{"input":{}}' through B
    Then the answer's edges are the runs "run"
    When token "m" sends "POST /api/v1/stacks/infra/runs/{run}/cancel" with '{}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_CANCEL_BLOCKING"'
    When token "m" sends "POST /api/v1/stacks/infra/runs/{run}/priority-set" with '{"preset":"low"}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_PRIORITIZE_SET"'

  @op:run @op:runStack @op:runCancel @op:runPrioritySet @op:searchRuns @op:searchRunsSuggestions @op:stackRuns @op:stackRun @case:failure
  Scenario: An unknown run, a cancel of a run that ended, a run named on another stack and a malformed search are refused
    Given token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED"}' through A
    And the answer's id names "run"
    When token "h" sends "GET /api/v1/runs/01JBQ6Q4V2ZK5Y3W8N7M6P5R4T" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/runs/nope/stack" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks/hidden/runs/{run}/cancel" with '{}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/stacks/hidden/run?id={run}" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/cancel" with '{}' through A
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/cancel" with '{}' through B
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/priority-set" with '{"preset":"high"}' through A
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/priority-set" with '{"preset":"urgent"}' through B
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "GET /api/v1/stacks/infra/runs?before=nope" through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "#/query/before" as "format"
    When token "h" sends "POST /api/v1/runs/search" with '{"input":{"predicates":[{"field":"owner","constraint":{"stringEquals":["x"]}}]}}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/input/predicates/0/field" as "unknown-field"
    When token "h" sends "POST /api/v1/runs/search" with '{"input":{"after":"x"},"offset":2}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/offset" as "rule"
    When token "h" sends "POST /api/v1/runs/search/suggestions" with '{"predicates":[{"field":"state","constraint":{"stringEquals":["x"]}}]}' through B
    Then it answers a 422 problem "validation-failed"
