@policy-push-commits @postgres @objectstore @github @gitlab
Feature: Native push commit collections retain completeness
  The fake provider's signed delivery uses metadata from actual Git objects.
  GitLab's documented newest-twenty limit never becomes a complete policy list.
  ref: decision 0009
  ref: decision 0012
  ref: decision 0016

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"

  Scenario Outline: <provider> projects complete commit facts and omits <omission>
    Given a "<provider>" plan-comment stack "infra" using "terraform" with labels '[]'
    And token "h" sends "POST /api/v1/policies" with '{"name":"Native commits","type":"GIT_PUSH","body":"package infraweave\npropose if { count(input.push.commits) == 2; every c in input.push.commits { c.affected_files == [\"main.tf\"]; c.hash != \"\"; c.created_at > 0; c.message != \"\"; c.author != \"\"; c.branch == \"feature\" } }\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    And the event repository prepares <commits> real native commits on "feature" as "collected"
    When the event fixture pushes "native" from "seed" to "collected" on "feature" with "valid" verification through B and receives 202
    Then event delivery "native" settles with <runs> new runs
    And event delivery "native" retains <retained> native commits with omission "<omission>"

    Examples:
      | provider | commits | runs | retained | omission              |
      | GitHub   | 2       | 1    | 2        |                       |
      | GitLab   | 2       | 1    | 2        |                       |
      | GitLab   | 21      | 0    | 20       | provider-commit-limit |

  Scenario Outline: <provider> keeps a reverted native path in affected files and stack counts
    Given a "<provider>" plan-comment stack "infra" using "terraform" with labels '[]'
    And token "h" sends "POST /api/v1/policies" with '{"name":"Reverted native path","type":"GIT_PUSH","body":"package infraweave\ntrack if { input.push.affected_files == [\"main.tf\"]; input.push.affected_stacks_count == 1; count(input.push.commits) == 2 }\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    And the event repository prepares a real native edit and revert on "feature" as "collected"
    When the event fixture pushes "native" from "seed" to "collected" on "feature" with "valid" verification through B and receives 202
    Then event delivery "native" settles with 1 new runs
    And event delivery "native" retains 2 native commits with omission ""

    Examples:
      | provider |
      | GitHub   |
      | GitLab   |
