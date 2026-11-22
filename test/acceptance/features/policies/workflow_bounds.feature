@policies @postgres @vcs @policy-approval
Feature: Actual workflow decisions retain bounded messages and private prints
  OPA evaluates oversized Unicode notes and more than 64 KiB of print output.
  The public review commits bounded UTF-8 notes while private printing neither
  changes the approval nor enters retained decisions, responses or process logs.
  ref: decision 0009
  ref: decision 0016

  @op:runReview @case:success
  Scenario: A real review bounds oversized notes without exposing private prints
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy"},"manageState":false}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And token "h" sends "POST /api/v1/policies" with '{"name":"Bounded notes","type":"APPROVAL","space":"legacy","body":"package infraweave\nlong := concat(\"\", [sprintf(\"世%d\", [i]) | some i in numbers.range(0,1499)])\napprove if { count(input.reviews.current.approvals) > 0; every i in numbers.range(0,31) { print(\"private-policy-print-654\", i, long) } }\napprove_with_note contains sprintf(\"%03d:%s\", [i,long]) if { count(input.reviews.current.approvals) > 0; some i in numbers.range(0,104) }\nflag contains \"bounded\" if count(input.reviews.current.approvals) > 0\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    Then it answers 200
    And the answer's id names "run"
    When token "h" sends "POST /api/v1/stacks/infra/runs/review" with '{"run":"{run}","decision":"APPROVE"}' through B
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "READY" through A
    And the answer's flags is '["bounded"]'
    And the committed workflow update of run "run" matches the public policy snapshot
    And the workflow stores 100 messages of at most 4096 bytes for policy "policy" on run "run"
