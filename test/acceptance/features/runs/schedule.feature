@postgres @vcs
Feature: A stack's queued runs are scheduled in one evaluation
  Each run a trigger creates enqueues its stack's run.schedule job, and a
  job still pending takes the next one in, so a burst of runs on one stack
  is scheduled by one evaluation. The two servers run no default-queue
  jobs, so the burst waits until a third server that runs them starts.
  The stack's state follows its tracked run: NONE while the run is queued,
  READY once it takes the lock; stack search filters and sorts by it.
  The home key is space-admin on the account's top space.

  @op:runTrigger @op:searchStacks @case:success
  Scenario: A burst of runs on one stack is scheduled by one evaluation, and the stack's state follows its tracked run
    Given two infraweave servers, A and B, on one database, that run no default-queue jobs
    And the home key is exchanged through A for token "h"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And a commit to "main" of "infra" changing "main.tf"
    And the head of "main" of "infra" is kept as "pushed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy"},"manageState":false}' through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Idle","repository":"idle","branch":"main","space":"legacy"},"manageState":false}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "first"
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{pushed}"}' through B
    And the answer's id names "second"
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And the answer's id names "proposal"
    Then the "run.schedule" jobs are 1, pending
    And token "h" searching the stacks where '[{"field":"state","constraint":{"enumEquals":["NONE"]}}]' through B in pages of 2 finds "idle, infra" forwards and back
    When a third infraweave server runs the default queue's jobs
    Then token "h" eventually reads the run "first" in the state "READY" through A
    And token "h" eventually reads the run "proposal" in the state "READY" through B
    And the "run.schedule" jobs are 1, succeeded
    When token "h" sends "GET /api/v1/runs/{second}" through A
    Then the answer's state is '"QUEUED"'
    And token "h" searching the stacks where '[{"field":"state","constraint":{"enumEquals":["READY"]}}]' through B in pages of 2 finds "infra" forwards and back
    And token "h" searching the stacks by state through A in pages of 2 finds "idle, infra" forwards and back
    And token "h" searching the stacks by state descending through B in pages of 2 finds "infra, idle" forwards and back
