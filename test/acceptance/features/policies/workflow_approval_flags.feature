@postgres @vcs @policy-approval
Feature: Public reviews preserve independent flag owners and reset ordering
  Both stored dialects run through the actual approval gate. One owner's reset
  cannot remove another owner's shared flag; a later evaluation resets and
  adds that same flag before the other owner removes its own copy.
  ref: decision 0009
  ref: decision 0016

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy"},"manageState":false}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And the member key is space-writer on the space "legacy"
    And the member key is exchanged through B for token "m"
    And token "h" sends "POST /api/v1/policies" with '{"name":"Alpha","type":"APPROVAL","space":"legacy","engineType":"REGO_V1","body":"package infraweave\nn := count(input.reviews.current.approvals)\napprove if n >= 2\nflag contains \"a-old\" if n == 0\nflag contains \"shared\" if n == 0\nflag contains \"a-new\" if n == 1\nflag contains \"a-last\" if n >= 2\nflag contains \"shared\" if n >= 2\nflag contains \"saw-initial\" if { n == 1; \"a-old\" in input.run.flags }\nflag contains \"saw-later\" if { n >= 2; \"a-new\" in input.run.flags }\nreset_flag contains \"a-old\" if n >= 1\nreset_flag contains \"shared\" if n >= 1\nreset_flag contains \"a-new\" if n >= 2\nreset_flag contains \"saw-initial\" if n >= 2\n"}' through A
    And it answers 201
    And the answer's id names "Alpha"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{Alpha}" through B
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '{"name":"Beta","type":"APPROVAL","space":"legacy","engineType":"REGO_V0","body":"package infraweave\nimport future.keywords.in\nn := count(input.reviews.current.approvals)\napprove { n >= 2 }\nflag[\"shared\"] { n < 2 }\nflag[\"b-old\"] { n < 2 }\nflag[\"b-last\"] { n >= 2 }\nreset_flag[\"shared\"] { n >= 2 }\nreset_flag[\"b-old\"] { n >= 2 }\n"}' through A
    And it answers 201
    And the answer's id names "Beta"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{Beta}" through B
    And it answers 200

  @op:runReview @case:success
  Scenario: Actual reviews reset one owner before adding flags without losing another owner
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads run "run" flags '["a-old","b-old","shared"]' through B
    And the answer's state is '"QUEUED"'
    And the answer's needsApproval is 'true'
    When token "h" sends "POST /api/v1/stacks/infra/runs/review" with '{"run":"{run}","decision":"APPROVE","note":"First flag snapshot"}' through A
    Then it answers 200
    And token "h" eventually reads run "run" flags '["a-new","b-old","saw-initial","shared"]' through B
    And the answer's state is '"QUEUED"'
    And the answer's needsApproval is 'true'
    When token "m" sends "POST /api/v1/stacks/infra/runs/review" with '{"run":"{run}","decision":"APPROVE","note":"Second flag snapshot"}' through B
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "READY" through A
    And token "h" eventually reads run "run" flags '["a-last","b-last","saw-later","shared"]' through B
    And the answer's needsApproval is 'false'
    And the answer's review decisions are "APPROVE, APPROVE"
