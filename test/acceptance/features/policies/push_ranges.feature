@policy-push-range @postgres @objectstore @github
Feature: Push range paths survive missing native commit collections
  Source preparation must retain every touched path in the immutable pushed
  range, including an intermediate edit reverted before the final commit.
  ref: decision 0009
  ref: decision 0012
  ref: decision 0016

  Scenario: A real GitHub edit and revert still selects its affected stack without native commits
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a "GitHub" plan-comment stack "infra" using "terraform" with labels '[]'
    And token "h" sends "POST /api/v1/policies" with '{"name":"Touched range","type":"GIT_PUSH","body":"package infraweave\ntrack if { input.push.affected_files == [\"main.tf\"]; input.push.affected_stacks_count == 1 }\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    And the event repository prepares a real native edit and revert on "main" as "collected"
    And the GitHub event push omits its native commit collection
    When the event fixture pushes "range" from "seed" to "collected" on "main" with "valid" verification through B and receives 202
    Then event delivery "range" settles with 1 new runs
