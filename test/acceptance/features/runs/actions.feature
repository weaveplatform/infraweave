@postgres @vcs
Feature: Runs are retried, promoted, discarded together and commented on
  A caller retries a proposed or tracked run once it has ended: a new run
  of its type on its commit, whose first transition names the run
  retried. A tracked run's retry also needs RUN_RETRY_BLOCKING. A caller
  promotes a proposed run whose commit is newer than the stack's tracked
  commit, on a stack whose githubActionDeploy allows it: the commit
  becomes the tracked commit, and a tracked run on it is queued on the
  stack's branch. A run's canRetry and canPromote say whether it can be,
  and its retryBlocker and promoteBlocker why not. Discarding all of a
  stack's runs, which the confirmation feature shows succeeding, needs
  RUN_DISCARD on the stack's space. A caller who
  reads a run comments on it, and the run answers its comments oldest
  first. A stack the caller cannot read, or another account's, answers as
  one that does not exist. The home key is space-admin on the account's
  top space; the member key holds only the bindings a scenario gives it.
  Stack infra lives in legacy and stack hidden in team.
  ref: decision 0008

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy"},"manageState":false}' through B
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A

  @op:runRetry @op:runCancel @case:success
  Scenario: Retrying an ended run queues a run of its type on its commit, which names the run retried
    Given token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And the answer's id names "proposal"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{proposal}/cancel" with '{}' through B
    Then it answers 200
    And the answer's canRetry is 'true'
    And the answer's canPromote is 'false'
    And the answer's allowedActions is '["RUN_RETRY","RUN_COMMENT"]'
    When token "h" sends "POST /api/v1/stacks/infra/runs/{proposal}/retry" through A
    Then it answers 200
    And the answer drew on the expensive budget
    And the answer's type is '"PROPOSED"'
    And the answer's state is '"QUEUED"'
    And the answer's commits is '["{seed}"]'
    And the answer's canRetry is 'false'
    And the answer's retryBlocker is '"A retry needs a run that has ended."'
    And the answer's last state transition's note is "retry of {proposal}"

  @op:runRetry @case:denial
  Scenario: A tracked run's retry needs RUN_RETRY_BLOCKING, which a space reader lacks
    Given token "h" sends "POST /api/v1/stacks/infra/runs" with '{}' through A
    And the answer's id names "tracked"
    And token "h" eventually reads the run "tracked" in the state "READY" through B
    And token "h" sends "POST /api/v1/stacks/infra/runs/{tracked}/cancel" with '{}' through A
    When token "m" sends "POST /api/v1/stacks/infra/runs/{tracked}/retry" through B
    Then it answers a 404 problem "not-found"
    Given the member key is space-reader on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/runs/{tracked}/retry" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_RETRY_BLOCKING"'
    Given the member key is space-writer on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/runs/{tracked}/retry" through B
    Then it answers 200
    And the answer's type is '"TRACKED"'

  @op:runRetry @case:failure
  Scenario: A run that has not ended, and one that does not exist, are not retried
    Given token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And the answer's id names "proposal"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{proposal}/retry" through B
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "POST /api/v1/stacks/infra/runs/01ARZ3NDEKTSV4RRFFQ69G5FAV/retry" through A
    Then it answers a 404 problem "not-found"

  @op:runPromote @case:success
  Scenario: Promoting a proposed run on a newer commit tracks the commit and queues a tracked run on it
    Given a commit to "feature" of "infra" changing "main.tf"
    And the head of "feature" of "infra" is kept as "pushed"
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{pushed}"}' through A
    And the answer's id names "proposal"
    And the answer's canPromote is 'true'
    And the answer's allowedActions is '["RUN_DISCARD","RUN_CANCEL","RUN_PRIORITIZE_SET","RUN_PROMOTE","RUN_COMMENT"]'
    When token "h" sends "POST /api/v1/stacks/infra/runs/{proposal}/promote" through B
    Then it answers 200
    And the answer drew on the expensive budget
    And the answer's type is '"TRACKED"'
    And the answer's commits is '["{pushed}"]'
    And the answer's branch is '"main"'
    And the answer's last state transition's note is "promotion of {proposal}"
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then it answers 200
    And the answer tracks the commit kept as "pushed" at the branch head kept as "seed"
    And the answer's tracked commit was not set by infraweave
    When token "h" sends "POST /api/v1/stacks/infra/runs/{proposal}/promote" through A
    Then it answers a 409 problem "invalid-state"

  @op:runPromote @case:denial
  Scenario: Promoting needs RUN_PROMOTE, which a space reader lacks
    Given a commit to "feature" of "infra" changing "main.tf"
    And the head of "feature" of "infra" is kept as "pushed"
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{pushed}"}' through A
    And the answer's id names "proposal"
    When token "m" sends "POST /api/v1/stacks/infra/runs/{proposal}/promote" through B
    Then it answers a 404 problem "not-found"
    Given the member key is space-reader on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/runs/{proposal}/promote" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_PROMOTE"'
    Given the member key is space-writer on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/runs/{proposal}/promote" through B
    Then it answers 200

  @op:runPromote @case:failure
  Scenario: A stack that does not allow promotion, and a tracked run, are not promoted
    Given a commit to "feature" of "infra" changing "main.tf"
    And the head of "feature" of "infra" is kept as "pushed"
    And token "h" sends "PATCH /api/v1/stacks/infra" with '{"githubActionDeploy":false}' through A
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{pushed}"}' through B
    And the answer's id names "proposal"
    And the answer's canPromote is 'false'
    And the answer's promoteBlocker is '"A promotion needs a stack that allows run promotion."'
    And token "h" eventually reads the run "proposal" in the state "READY" through A
    When token "h" sends "POST /api/v1/stacks/infra/runs/{proposal}/promote" through A
    Then it answers a 409 problem "invalid-state"
    And the answer's detail is '"The run is READY; a promotion needs a stack that allows run promotion."'
    Given token "h" sends "POST /api/v1/stacks/infra/runs" with '{}' through B
    And the answer's id names "tracked"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{tracked}/promote" through A
    Then it answers a 409 problem "invalid-state"

  @op:runDiscardAll @case:denial
  Scenario: Discarding all needs RUN_DISCARD, which a space reader lacks
    When token "m" sends "POST /api/v1/stacks/infra/runs/discard-all" with '{}' through A
    Then it answers a 404 problem "not-found"
    Given the member key is space-reader on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/runs/discard-all" with '{}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_DISCARD"'
    Given the member key is space-writer on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/runs/discard-all" with '{}' through A
    Then it answers 200

  @op:runDiscardAll @case:failure
  Scenario: An unknown stack and an unknown field are refused
    When token "h" sends "POST /api/v1/stacks/nope/runs/discard-all" with '{}' through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks/infra/runs/discard-all" with '{"runs":[]}' through B
    Then it answers a 400 problem "invalid-request"

  @op:runComment @op:run @case:success
  Scenario: Comments on a run are answered with it, oldest first
    Given token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And the answer's id names "proposal"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{proposal}/comment" with '{"body":"Looks good."}' through B
    Then it answers 200
    And the answer's body is '"Looks good."'
    Given the member key is space-reader on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/runs/{proposal}/comment" with '{"body":"Ship it."}' through A
    Then it answers 200
    When token "h" sends "GET /api/v1/runs/{proposal}" through B
    Then the answer's comment bodies are '["Looks good.","Ship it."]'

  @op:runComment @case:denial
  Scenario: Commenting needs RUN_COMMENT, which a role that only reads lacks
    Given token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And the answer's id names "proposal"
    And token "h" sends "POST /api/v1/roles" with '{"name":"Viewers","actions":["SPACE_READ"]}' through B
    When token "m" sends "POST /api/v1/stacks/infra/runs/{proposal}/comment" with '{"body":"Hello"}' through A
    Then it answers a 404 problem "not-found"
    Given the member key is viewers on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/runs/{proposal}/comment" with '{"body":"Hello"}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_COMMENT"'

  @op:runComment @case:failure
  Scenario: An empty comment, and one on a run that does not exist, are refused
    Given token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And the answer's id names "proposal"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{proposal}/comment" with '{"body":""}' through B
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{proposal}/comment" through A
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "POST /api/v1/stacks/infra/runs/01ARZ3NDEKTSV4RRFFQ69G5FAV/comment" with '{"body":"Hello"}' through B
    Then it answers a 404 problem "not-found"
