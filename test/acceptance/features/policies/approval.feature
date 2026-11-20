@postgres @vcs @policy-approval
Feature: State-scoped approval policies and run reviews
  An attached approval policy gates QUEUED before the worker can claim it.
  A review belongs to the state and version in which the authorized caller made
  it. A later state requires its own decision; evaluation receipts and flags
  commit with the decision, and approving UNCONFIRMED still requires confirmation.
  ref: decision 0009
  ref: decision 0008

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy"},"manageState":false}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And token "h" sends "POST /api/v1/policies" with '{"name":"State review","type":"APPROVAL","space":"legacy","engineType":"REGO_V1","body":"package infraweave\napprove if { count(input.reviews.current.approvals) > 0 }\nflag contains \"queued-reviewed\" if { approve }\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And it answers 200
    And the answer's id names "run"

  @op:runReview @case:success
  Scenario: An authorized queued review evaluates the real policy and releases the run
    When token "h" sends "POST /api/v1/stacks/infra/runs/review" with '{"run":"{run}","decision":"APPROVE","note":"Queued review"}' through B
    Then it answers 200
    And the answer's decision is '"APPROVE"'
    And the answer's note is '"Queued review"'
    And the answer's state is '"QUEUED"'
    And the answer's stateVersion is '1'
    And token "h" eventually reads the run "run" in the state "READY" through A
    And the answer's flags is '["queued-reviewed"]'

  @op:runReview @case:denial
  Scenario: A stack reader cannot supply a review
    Given the member key is space-reader on the space "legacy"
    And the member key is exchanged through B for token "m"
    When token "m" sends "POST /api/v1/stacks/infra/runs/review" with '{"run":"{run}","decision":"APPROVE"}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_REVIEW"'

  @op:runReview @case:failure
  Scenario: Unknown runs and runs outside review states refuse a review
    When token "h" sends "POST /api/v1/stacks/infra/runs/review" with '{"run":"01ARZ3NDEKTSV4RRFFQ69G5FAV","decision":"APPROVE"}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/cancel" with '{}' through A
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs/review" with '{"run":"{run}","decision":"APPROVE"}' through B
    Then it answers a 409 problem "invalid-state"
