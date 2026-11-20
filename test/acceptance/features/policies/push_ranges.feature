@policy-push-range @postgres @objectstore @github @gitlab @bitbucket-cloud @bitbucket-dc @azure-devops
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

  Scenario Outline: <provider> recovers <metadata> commit data without losing intermediate reverted paths
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a "<provider>" plan-comment stack "infra" using "terraform" with labels '[]'
    And token "h" sends "POST /api/v1/policies" with '{"name":"Complete range","type":"GIT_PUSH","body":"package infraweave\ntrack if { input.push.branch == \"main\"; input.push.affected_files == [\"main.tf\"]; input.push.affected_stacks_count == 1; count(input.push.commits) == <commits>; every c in input.push.commits { c.affected_files == [\"main.tf\"]; c.hash != \"\"; c.created_at > 0; c.author != \"\"; c.message != \"\" } }\nflag contains \"pushed\"\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    And token "h" sends "GET /api/v1/policies/{policy}" through A
    And the policy version for "policy" is kept from its ETag
    And the event repository prepares a real native edit and revert on "main" as "collected"
    And the event repository prepares <extra> real native commits on "main" as "collected"
    And the event push uses "<metadata>" native commit metadata
    When the event fixture pushes "range" from "seed" to "collected" on "main" with "valid" verification through B and receives 202
    Then event delivery "range" settles with 1 new runs
    And event delivery "range" retains a recovered "main" range of <commits> commits and touched paths '["main.tf"]' with native omission "<omission>"
    And the "TRACKED" event run on stack "infra" for commit "collected" is kept as "run"
    When token "h" sends "GET /api/v1/runs/{run}" through A
    Then the answer carries initial push evidence for policy "policy"
    And the answer's flags is '["pushed"]'
    When the event fixture pushes "redelivery" from "seed" to "collected" on "main" with "valid" verification through A and receives 202
    Then event delivery "redelivery" settles with 0 new runs
    And stack "infra" has exactly 1 event runs for commit "collected"

    Examples:
      | provider        | metadata | extra | commits | omission                 |
      | GitHub          | missing  | 0     | 2       | absent                   |
      | GitLab          | missing  | 0     | 2       | absent                   |
      | Bitbucket Cloud | missing  | 0     | 2       | absent                   |
      | Bitbucket DC    | missing  | 0     | 2       | absent                   |
      | Azure DevOps    | missing  | 0     | 2       | absent                   |
      | GitHub          | native   | 2046  | 2048    | provider-commit-limit    |
      | GitLab          | native   | 19    | 21      | provider-commit-limit    |
      | Bitbucket Cloud | native   | 4     | 6       | provider-commit-limit    |
      | Bitbucket Cloud | native   | 0     | 2       | missing-commit-files     |
      | Azure DevOps    | native   | 0     | 2       | missing-commit-files     |
      | Azure DevOps    | multi-ref| 0     | 2       | multiple-ref-updates     |
