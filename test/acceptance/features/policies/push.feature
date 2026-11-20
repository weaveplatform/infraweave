@postgres @objectstore @gitlab
Feature: Git push policy workflow
  Stored verified deliveries evaluate attached push policies under the stack's
  creation and queue fences. A custom policy replaces the default push behavior.
  ref: decision 0009
  ref: decision 0012
  ref: decision 0016

  Scenario: A custom ignore suppresses a tracked push and records its policy reason
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
    And token "h" sends "POST /api/v1/policies" with '{"name":"Freeze pushes","type":"GIT_PUSH","body":"package infraweave\nignore := true\n"}' through A
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
