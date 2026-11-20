@postgres @vcs @policy-approval
Feature: Public policy gates enforce actual OPA execution budgets
  An expensive stored program executes only after an actual review. Its
  deadline must defeat another policy's approval and commit safe failure
  evidence without committing the expensive program's partial flags.
  ref: decision 0009
  ref: decision 0016

  @op:runReview @case:failure
  Scenario Outline: The <budget> deadline keeps a reviewed run queued
    Given the workflow policies allow <each> ms per evaluation and <combined> ms combined
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy"},"manageState":false}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And token "h" sends "POST /api/v1/policies" with '{"name":"Cheap approval","type":"APPROVAL","space":"legacy","body":"package infraweave\napprove if true\nflag contains \"other-approved\" if true\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '{"name":"Expensive approval","type":"APPROVAL","space":"legacy","body":"package infraweave\nvalues := numbers.range(0,299)\napprove if {\n count(input.reviews.current.approvals) > 0\n some a in values\n some b in values\n some c in values\n some d in values\n a+b+c+d == -1\n}\nflag contains \"expensive-result-must-not-commit\" if count(input.reviews.current.approvals) > 0\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    Then it answers 200
    And the answer's id names "run"
    When token "h" sends "POST /api/v1/stacks/infra/runs/review" with '{"run":"{run}","decision":"APPROVE"}' through B
    Then it answers 200
    When token "h" sends "GET /api/v1/runs/{run}" through A
    Then it answers 200
    And the answer's state is '"QUEUED"'
    And the answer's needsApproval is 'true'
    And the answer's flags is '["other-approved"]'
    And the answer contains a workflow "APPROVAL" error "TIMEOUT"
    And the committed workflow update of run "run" matches the public policy snapshot

    Examples:
      | budget     | each | combined |
      | individual | 500  | 2000     |
      | combined   | 2000 | 150      |
