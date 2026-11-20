@policy-push-locks @postgres @objectstore @github @gitlab @bitbucket-cloud @bitbucket-dc @azure-devops
Feature: Push policy locks join new run creation
  A policy lock is committed with its new run. Existing admitted runs may
  schedule; a subsequent matching unlock admits another run and clears the lock.
  ref: decision 0008
  ref: decision 0009
  ref: decision 0012
  ref: decision 0016

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"

  Scenario Outline: <provider> commits a policy lock and matching unlock with its runs
    Given a "<provider>" plan-comment stack "infra" using "terraform" with labels '[]'
    And token "h" sends "POST /api/v1/policies" with '{"name":"Deployment lock","type":"GIT_PUSH","body":"package infraweave\ntrack := true\nlock := \"deploy\"\nflag contains \"pushed\"\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    And token "h" sends "GET /api/v1/policies/{policy}" through A
    And the policy version for "policy" is kept from its ETag
    When the event repository commits "locked" on "main"
    And the event fixture pushes "lock" from "seed" to "locked" on "main" with "valid" verification through B and receives 202
    Then event delivery "lock" settles with 1 new runs
    And the "TRACKED" event run on stack "infra" for commit "locked" is kept as "first"
    When token "h" eventually reads the run "first" in the state "READY" through A
    Then the answer carries initial push evidence for policy "policy"
    And the answer's state is '"READY"'
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then the answer's lockedBy is '"deploy"'
    And the answer's lockNote is '"locked by push policy"'
    When token "h" sends "PATCH /api/v1/policies/{policy}" with '{"body":"package infraweave\ntrack := true\nunlock := \"deploy\"\n"}' through A
    Then it answers 200
    When the event repository commits "unlocked" on "main"
    And the event fixture pushes "unlock" from "locked" to "unlocked" on "main" with "valid" verification through A and receives 202
    Then event delivery "unlock" settles with 1 new runs
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then the event stack is unlocked in its public read and owner storage
    And token "h" eventually reads the run "first" in the state "READY" through A
    And stack "infra" has exactly 1 event runs for commit "unlocked"

    Examples:
      | provider        |
      | GitHub          |
      | GitLab          |
      | Bitbucket Cloud |
      | Bitbucket DC    |
      | Azure DevOps    |
