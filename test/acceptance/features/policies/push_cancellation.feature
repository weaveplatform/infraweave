@policy-push @postgres @objectstore @github @gitlab @bitbucket-cloud @bitbucket-dc @azure-devops
Feature: Push policies cancel eligible queued runs
  Replacement creation, initial evidence and cancellation share the stack lock
  and child completion transaction. Repeated deliveries do not repeat effects.
  ref: decision 0008
  ref: decision 0009
  ref: decision 0012
  ref: decision 0016

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"

  Scenario Outline: A queued cancellation retains the unrelated blocking run
    Given a "<provider>" plan-comment stack "infra" using "terraform" with labels '[]'
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And it answers 200
    And the answer's id names "blocking"
    And token "h" eventually reads the run "blocking" in the state "READY" through B
    And the event repository commits "queued" on "main"
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{queued}"}' through B
    And it answers 200
    And the answer's id names "prior"
    And token "h" eventually reads the run "prior" in the state "QUEUED" through A
    And token "h" sends "POST /api/v1/policies" with '{"name":"Replace queued","type":"GIT_PUSH","body":"package infraweave\ntrack := true\ncancel contains run.id if { run := input.in_progress[_]; run.commit.hash == \"{queued}\" }\nflag contains \"pushed\"\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    And token "h" sends "GET /api/v1/policies/{policy}" through A
    And the policy version for "policy" is kept from its ETag
    When the event repository commits "replacement" on "main"
    And the event fixture pushes "replace" from "queued" to "replacement" on "main" with "valid" verification through B and receives 202
    Then event delivery "replace" settles with 1 new runs
    And the "TRACKED" event run on stack "infra" for commit "replacement" is kept as "run"
    And run "prior" records push cancellation in favor of "run" with state "CANCELED" through A
    And token "h" eventually reads the run "blocking" in the state "READY" through B
    When token "h" sends "GET /api/v1/runs/{run}" through A
    Then the answer carries initial push evidence for policy "policy"
    When the event fixture pushes "again" from "queued" to "replacement" on "main" with "valid" verification through A and receives 202
    Then event delivery "again" settles with 0 new runs
    And run "prior" records push cancellation in favor of "run" with state "CANCELED" through B

    Examples:
      | provider        |
      | GitHub          |
      | GitLab          |
      | Bitbucket Cloud |
      | Bitbucket DC    |
      | Azure DevOps    |

  Scenario Outline: A ready cancellation releases the blocking run lock for its replacement
    Given a "<provider>" plan-comment stack "infra" using "terraform" with labels '[]'
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And it answers 200
    And the answer's id names "prior"
    And token "h" eventually reads the run "prior" in the state "READY" through B
    And token "h" sends "POST /api/v1/policies" with '{"name":"Replace ready","type":"GIT_PUSH","body":"package infraweave\ntrack := true\ncancel contains run.id if { run := input.in_progress[_]; run.id == \"{prior}\" }\nflag contains \"pushed\"\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When the event repository commits "replacement" on "main"
    And the event fixture pushes "replace" from "seed" to "replacement" on "main" with "valid" verification through B and receives 202
    Then event delivery "replace" settles with 1 new runs
    And the "TRACKED" event run on stack "infra" for commit "replacement" is kept as "run"
    And run "prior" records push cancellation in favor of "run" with state "CANCELED" through A
    And token "h" eventually reads the run "run" in the state "READY" through B

    Examples:
      | provider        |
      | GitHub          |
      | GitLab          |
      | Bitbucket Cloud |
      | Bitbucket DC    |
      | Azure DevOps    |
