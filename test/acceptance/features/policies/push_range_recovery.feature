@policy-push-range @postgres @objectstore @github @gitlab @bitbucket-cloud @bitbucket-dc @azure-devops
Feature: Native range failures preserve retry and deadline boundaries
  Source refusal happens before preparation, child creation or policy effects.
  A real queue retry recovers the complete range once; expiry cannot become a
  successful no-op or publish partial policy consequences.
  ref: decision 0009
  ref: decision 0011
  ref: decision 0012
  ref: decision 0016

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"

  Scenario Outline: <provider> recovers a <fault> without provisional effects or duplicate admission
    Given a "<provider>" plan-comment stack "infra" using "terraform" with labels '[]'
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And it answers 200
    And the answer's id names "prior"
    And token "h" eventually reads the run "prior" in the state "READY" through B
    And token "h" sends "POST /api/v1/policies" with '{"name":"Recovered replacement","type":"GIT_PUSH","body":"package infraweave\ntrack := true\nlock := \"range\"\ncancel contains \"{prior}\"\nflag contains \"pushed\"\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    And token "h" sends "GET /api/v1/policies/{policy}" through A
    And the policy version for "policy" is kept from its ETag
    And the event repository prepares a real native edit and revert on "main" as "collected"
    And the event push uses "missing" native commit metadata
    And the event range source refuses with "<fault>"
    When the event fixture pushes "retry" from "seed" to "collected" on "main" with "valid" verification through B and receives 202
    Then event delivery "retry" waits for source recovery with no provisional effects
    And token "h" eventually reads the run "prior" in the state "READY" through A
    And the answer's flags is '[]'
    And the event stack is unlocked in its public read and owner storage
    When the event range source recovers and delivery "retry" becomes due
    Then event delivery "retry" settles with 1 new runs
    And event delivery "retry" retains a recovered "main" range of 2 commits and touched paths '["main.tf"]' with native omission "absent"
    And the "TRACKED" event run on stack "infra" for commit "collected" is kept as "run"
    And run "prior" records push cancellation in favor of "run" with state "CANCELED" through B
    When token "h" sends "GET /api/v1/runs/{run}" through A
    Then the answer carries initial push evidence for policy "policy"
    And the answer's flags is '["pushed"]'
    When the event fixture pushes "duplicate" from "seed" to "collected" on "main" with "valid" verification through A and receives 202
    Then event delivery "duplicate" settles with 0 new runs
    And stack "infra" has exactly 1 event runs for commit "collected"

    Examples:
      | provider        | fault          |
      | GitHub          | rate limit     |
      | GitLab          | rate limit     |
      | Bitbucket Cloud | rate limit     |
      | Bitbucket DC    | rate limit     |
      | Azure DevOps    | rate limit     |
      | GitHub          | server failure |
      | GitLab          | server failure |
      | Bitbucket Cloud | server failure |
      | Bitbucket DC    | server failure |
      | Azure DevOps    | server failure |

  Scenario Outline: <provider> expires a source-blocked delivery without admitting an empty range
    Given a "<provider>" plan-comment stack "infra" using "terraform" with labels '[]'
    And token "h" sends "POST /api/v1/policies" with '{"name":"No partial effects","type":"GIT_PUSH","body":"package infraweave\ntrack := true\nlock := \"range\"\nflag contains \"pushed\"\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    And the event repository prepares a real native edit and revert on "main" as "collected"
    And the event push uses "missing" native commit metadata
    And the event range source refuses with "rate limit"
    When the event fixture pushes "expired" from "seed" to "collected" on "main" with "valid" verification through B and receives 202
    Then event delivery "expired" waits for source recovery with no provisional effects
    When event delivery "expired" expires before source recovery
    Then stack "infra" has exactly 0 event runs for commit "collected"
    And the event stack is unlocked in its public read and owner storage

    Examples:
      | provider        |
      | GitHub          |
      | GitLab          |
      | Bitbucket Cloud |
      | Bitbucket DC    |
      | Azure DevOps    |
