@policies @postgres @vcs @policy-approval
Feature: Public reviews retain safe policy execution failures
  Real typed workflow inputs obey the configured execution limit. Malformed
  results and conflicting booleans cannot release a queued run or commit flags
  from an unsuccessful program, even when the public review itself succeeds.
  ref: decision 0009
  ref: decision 0016

  @op:runReview @case:failure
  Scenario Outline: A review retains <failure> without partial flags
    Given the workflow policy input budget is <limit> bytes
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy"},"manageState":false}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And token "h" sends "POST /api/v1/policies" with '{"name":"Bounded approval","type":"APPROVAL","space":"legacy","body":"<body>"}' through A
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
    And the answer's flags is '[]'
    And the answer contains a workflow "APPROVAL" error "<code>"
    And the committed workflow update of run "run" matches the public policy snapshot

    Examples:
      | failure              | limit | code      | body |
      | input overflow       | 64    | LIMIT     | package infraweave\\napprove if true\\nflag contains \\"partial-must-not-commit\\" if true\\n |
      | a malformed boolean  | 65536 | RESULT    | package infraweave\\napprove := 42 if count(input.reviews.current.approvals) > 0\\nflag contains \\"partial-must-not-commit\\" if count(input.reviews.current.approvals) > 0\\n |
      | conflicting booleans | 65536 | EXECUTION | package infraweave\\napprove := true if count(input.reviews.current.approvals) > 0\\napprove := false if count(input.reviews.current.approvals) > 0\\nflag contains \\"partial-must-not-commit\\" if count(input.reviews.current.approvals) > 0\\n |
