@policy-push-recovery @postgres @objectstore @github @gitlab @bitbucket-cloud @bitbucket-dc @azure-devops
Feature: Push policy creation and cancellation recover atomically
  Both native delivery paths race on a single commit. A real process crash
  after provisional effects but before queue completion rolls those effects
  back; restarted evaluators recover the persisted claims and commit once.
  ref: decision 0008
  ref: decision 0009
  ref: decision 0011
  ref: decision 0012
  ref: decision 0016

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"

  Scenario Outline: <provider> raced native request and branch push recover one atomic replacement
    Given a "<provider>" plan-comment stack "infra" using "terraform" with labels '[]'
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{commentBase}"}' through A
    And it answers 200
    And the answer's id names "prior"
    And token "h" eventually reads the run "prior" in the state "READY" through B
    And token "h" sends "POST /api/v1/policies" with '{"name":"Atomic replacement","type":"GIT_PUSH","body":"package infraweave\ntrack := true\ncancel contains run.id if { run := input.in_progress[_]; run.id == \"{prior}\" }\nflag contains \"pushed\"\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    And token "h" sends "GET /api/v1/policies/{policy}" through A
    And the policy version for "policy" is kept from its ETag
    And the event fixture holds future child evaluations
    And the event fixture blocks child transaction completion
    When native request and branch deliveries race across A and B
    And the event fixture releases child evaluations
    Then both event replicas die during child completion without partial run or cancellation
    When replacement event replicas recover the persisted child jobs
    Then the raced event deliveries settle with exactly one replacement
    And the "TRACKED" event run on stack "infra" for commit "seed" is kept as "run"
    And run "prior" records push cancellation in favor of "run" with state "CANCELED" through B
    And stack "infra" has exactly 1 event runs for commit "seed"
    When token "h" sends "GET /api/v1/runs/{run}" through A
    Then the answer carries initial push evidence for policy "policy"
    And the answer's flags is '["pushed"]'

    Examples:
      | provider        |
      | GitHub          |
      | GitLab          |
      | Bitbucket Cloud |
      | Bitbucket DC    |
      | Azure DevOps    |
