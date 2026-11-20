@terraform-plans @plan-sanitization @postgres @objectstore @vcs @docker
Feature: Worker plan sanitization
  The actual runner transforms only the server-readable JSON copy before
  sealing it. The tests open recorded objects with the server's artifact key,
  compare the typed policy projection, and inspect sealed bytes separately.
  A replan repeats sanitization and resumed apply still uses its raw workspace.
  ref: decision 0008
  ref: decision 0009

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  @terraform @tofu @terragrunt @op:runTrigger @op:runTargetedReplan @op:runConfirm @case:success
  Scenario Outline: A real <vendor> plan uses <mode>, sanitizes again on replan and applies its raw saved plan
    Given a Gitea repository "infra" with the files:
      | main.tf | variable "secret" {\n  type = string\n  sensitive = true\n}\nresource "terraform_data" "secret" {\n  input = { private = var.secret, public = "visible-642" }\n}\nresource "terraform_data" "copy" {\n  input = nonsensitive(var.secret)\n}\noutput "proof" {\n  value = terraform_data.secret.output.public\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","labels":<labels>,"vendorConfig":{"<vendor>":{"useSmartSanitization":<smart>}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_secret","type":"ENVIRONMENT_VARIABLE","value":"dummy-plan-secret-642","writeOnly":true}' through A
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And no object in the scenario's bucket holds "dummy-plan-secret-642", of at least 3 objects
    And the opened worker plan of the run "run" and its "<vendor>" policy projection use "<mode>" with units ""
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/targeted-replan" with '{"targets":["terraform_data.secret","terraform_data.copy"]}' through A
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "UNCONFIRMED" through B
    And the run "run" recorded 2 worker plan artifacts
    And the opened worker plan of the run "run" and its "<vendor>" policy projection use "<mode>" with units ""
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through A
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then the answer's outputs is '[{"id":"proof","sensitive":false,"value":"\"visible-642\""}]'

    Examples:
      | vendor    | smart | labels                                       | mode    |
      | terraform | false | []                                           | DEFAULT |
      | terraform | true  | []                                           | SMART   |
      | terraform | true  | ["feature:disable_resource_sanitization"]    | NONE    |
      | opentofu  | false | []                                           | DEFAULT |
      | opentofu  | true  | []                                           | SMART   |
      | opentofu  | true  | ["feature:disable_resource_sanitization"]    | NONE    |

  @terraform @tofu @terragrunt @op:runTrigger @op:runConfirm @case:success
  Scenario Outline: Terragrunt uploads its selected units under <mode> with run-all <all>
    Given a Gitea repository "infra" with the files:
      | root.hcl | locals {\n  owner = "infraweave"\n}\n |
      | live/terragrunt.hcl | include "root" {\n  path = find_in_parent_folders("root.hcl")\n}\n |
      | live/main.tf | variable "secret" {\n  type = string\n  sensitive = true\n}\nresource "terraform_data" "secret" {\n  input = { private = var.secret, public = "visible-642" }\n}\n |
      | live/child/terragrunt.hcl | include "root" {\n  path = find_in_parent_folders("root.hcl")\n}\n |
      | live/child/main.tf | variable "secret" {\n  type = string\n  sensitive = true\n}\nresource "terraform_data" "copy" {\n  input = nonsensitive(var.secret)\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","projectRoot":"live","space":"legacy","workerPool":"{pool}","labels":<labels>,"vendorConfig":{"terragrunt":{"tool":"<tool>","useRunAll":<all>,"useSmartSanitization":<smart>}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_secret","type":"ENVIRONMENT_VARIABLE","value":"dummy-plan-secret-642","writeOnly":true}' through A
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And the opened worker plan of the run "run" and its "terragrunt" policy projection use "<mode>" with units "<units>"
    And no object in the scenario's bucket holds "dummy-plan-secret-642", of at least 2 objects
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through A
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FINISHED" through B

    Examples:
      | tool           | all   | smart | labels                                    | mode    | units    |
      | TERRAFORM_FOSS | true  | false | []                                        | DEFAULT | ., child |
      | OPEN_TOFU      | true  | true  | []                                        | SMART   | ., child |
      | TERRAFORM_FOSS | false | true  | []                                        | SMART   | .        |
      | OPEN_TOFU      | false | true  | ["feature:disable_resource_sanitization"] | NONE    | .        |

  @terraform @op:runTrigger @case:failure
  Scenario: A sanitization failure fails planning without recording the original document
    Given a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "secret" {\n  input = "dummy-plan-secret-642"\n}\n |
      | .infraweave/workflow.yml | init: terraform init\nworkspaceSelect: terraform workspace select ${{ workspaceName }}\nworkspaceNew: terraform workspace new ${{ workspaceName }}\nplan: terraform plan -out=${{ planFileName }}\nshowState: terraform show -json\nshowPlan: >-\n  printf '%s' '{"format_version":"1.2","resource_changes":[{"address":"terraform_data.secret","mode":"managed","change":{"actions":["create"],"after":{"input":"dummy-plan-secret-642"},"after_sensitive":1}}]}'\ngetOutputs: terraform output -json\napply: terraform apply ${{ planFileName }}\ndestroy: terraform destroy\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FAILED" through A
    And the answer's last state transition's note is "The plan's plan.json could not be sealed: sanitize plan JSON: sanitize: malformed plan: invalid sensitivity marker."
    And the run "run" recorded 0 worker plan artifacts

  @terraform @op:runTrigger @case:failure
  Scenario: A permanent artifact declaration refusal fails planning without an original fallback
    Given a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "secret" {\n  input = "dummy-plan-secret-642"\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And the relay refuses plan uploads
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FAILED" through A
    And the answer's last state transition's note is "The plan's results could not be uploaded: declare uploads: 403 Forbidden."
    And the run "run" recorded 0 worker plan artifacts
