@postgres @objectstore @gitlab
Feature: Git push policy workflow
  Stored verified deliveries evaluate attached push policies under the stack's
  creation and queue fences. A custom policy replaces the default push behavior.
  ref: decision 0009
  ref: decision 0012
  ref: decision 0016

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a GitLab user "dev" with a token kept as "tok"
    And a GitLab project "acme/platform/infra" that "dev" maintains, its head of "main" kept as "initial"
    And the GitLab project "acme/platform/infra" commits files on "main" as "seed":
      | root/main.tf | resource "terraform_data" "this" {} |
    And token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"{tok}","customInput":{"name":"Push policies","spaceID":"acme","isDefault":true}}' through A
    And it answers 201
    And the answer's id names "integration"
    And the GitLab webhook secret is kept privately
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","namespace":"acme/platform","repository":"infra","branch":"main","provider":"GITLAB","space":"acme","projectRoot":"root","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
  Scenario: A custom ignore suppresses a tracked push and records its policy reason
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Freeze pushes","type":"GIT_PUSH","body":"package infraweave\nignore := true\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When the GitLab project "acme/platform/infra" commits files on "main" as "ignored":
      | root/main.tf | resource "terraform_data" "this" { input = "blocked" } |
    And GitLab pushes "ignoredPush" for integration "integration" from "seed" to "ignored" on "main" through A
    Then it answers 202
    And GitLab push "ignoredPush" for integration "integration" settles with 0 runs and 1 stack jobs
    When token "h" searches ignored triggers of stack "infra" with '{}' through B
    Then the ignored-trigger answer has 1 records for commit "ignored" with reason "CUSTOM_POLICY" and tag ""

  Scenario Outline: A selected policy without an explicit ignore has its own no-trigger reason
    Given token "h" sends "POST /api/v1/policies" with '{"name":"No automatic run","type":"GIT_PUSH","body":"package infraweave\n<body>\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When the GitLab project "acme/platform/infra" commits files on "main" as "ignored":
      | root/main.tf | resource "terraform_data" "this" { input = "no-trigger" } |
    And GitLab pushes "ignoredPush" for integration "integration" from "seed" to "ignored" on "main" through A
    Then it answers 202
    And GitLab push "ignoredPush" for integration "integration" settles with 0 runs and 1 stack jobs
    When token "h" searches ignored triggers of stack "infra" with '{}' through B
    Then the ignored-trigger answer has 1 records for commit "ignored" with reason "CUSTOM_POLICY_NO_TRIGGER" and tag ""
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then the answer tracks the commit kept as "<selected>" at the branch head kept as "ignored"

    Examples:
      | body                              | selected |
      | track if false                    | seed     |
      | track := true\\nnotrigger := true | ignored  |

  Scenario Outline: Custom decisions select a run type and persist initial evidence before scheduling
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Select event","type":"GIT_PUSH","engineType":"<engine>","body":"package infraweave\n<body>\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    And token "h" sends "GET /api/v1/policies/{policy}" through A
    And it answers 200
    And the policy version for "policy" is kept from its ETag
    When the GitLab project "acme/platform/infra" commits files on "main" as "selected":
      | <file> | event-policy-change |
    And GitLab pushes "selectedPush" for integration "integration" from "seed" to "selected" on "main" through A
    Then it answers 202
    And GitLab push "selectedPush" for integration "integration" settles with 1 runs and 1 stack jobs
    And the "<kind>" event run on stack "infra" for commit "selected" is kept as "run"
    When token "h" sends "GET /api/v1/runs/{run}" through B
    Then it answers 200
    And the answer's flags is '["pushed"]'
    And the answer carries initial push evidence for policy "policy"
    When GitLab pushes "sameCommit" for integration "integration" from "seed" to "selected" on "main" through B
    Then it answers 202
    And GitLab push "sameCommit" for integration "integration" settles with 0 runs and 1 stack jobs
    And stack "infra" has exactly 1 event runs for commit "selected"
    When token "h" sends "GET /api/v1/runs/{run}" through A
    Then the answer carries initial push evidence for policy "policy"

    Examples:
      | engine  | body                                                                           | file         | kind     |
      | REGO_V1 | track := true\\nflag contains \"pushed\"                                          | root/main.tf | TRACKED  |
      | REGO_V0 | propose := true\\nflag[\"pushed\"]                                               | root/main.tf | PROPOSED |
      | REGO_V1 | track if input.push.affected_stacks_count == 0\\nflag contains \"pushed\"         | outside.txt  | TRACKED  |

  Scenario: The explicit default-disable label suppresses only the default decision
    Given token "h" sends "PATCH /api/v1/stacks/infra" with '{"labels":["feature:disable_default_push_policy"]}' through B
    And it answers 200
    When the GitLab project "acme/platform/infra" commits files on "main" as "disabled":
      | root/main.tf | resource "terraform_data" "this" { input = "disabled" } |
    And GitLab pushes "disabledPush" for integration "integration" from "seed" to "disabled" on "main" through A
    Then it answers 202
    And GitLab push "disabledPush" for integration "integration" settles with 0 runs and 1 stack jobs
    When token "h" searches ignored triggers of stack "infra" with '{}' through B
    Then the ignored-trigger answer has 1 records for commit "disabled" with reason "DISABLED_BY_LABEL" and tag ""
    When token "h" sends "POST /api/v1/policies" with '{"name":"Explicit selection","type":"GIT_PUSH","body":"package infraweave\ntrack := true\n"}' through A
    Then it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When GitLab pushes "overridePush" for integration "integration" from "seed" to "disabled" on "main" through B
    Then it answers 202
    And GitLab push "overridePush" for integration "integration" settles with 1 runs and 1 stack jobs

  Scenario: A failed no-trigger decision commits a durable native check without creating a run
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Required event check","type":"GIT_PUSH","body":"package infraweave\nfail := true\nmessage contains \"event requires a release tag\"\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When the GitLab project "acme/platform/infra" commits files on "main" as "ignored":
      | root/main.tf | resource "terraform_data" "this" { input = "required-check" } |
    And GitLab pushes "ignoredPush" for integration "integration" from "seed" to "ignored" on "main" through A
    Then it answers 202
    And GitLab push "ignoredPush" for integration "integration" settles with 0 runs and 1 stack jobs
    And the ignored commit "ignored" has a durable policy check
    And the GitLab commit "ignored" has check "infraweave/infra/tracked" in state "failed" with message "event requires a release tag"
