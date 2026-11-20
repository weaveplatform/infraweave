@terraform-plans @plan-sanitization @postgres @objectstore @vcs @docker @terraform
Feature: Native action plan values are sanitized before upload
  The released Terraform and local provider actually produce these invocations.
  Planning does not invoke the action or apply a resource.
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

  @op:runTrigger @op:runTargetedReplan @case:success
  Scenario Outline: <mode> sanitizes a native action plan and repeats it on targeted replan
    Given a Gitea repository "infra" with the files:
      | main.tf | terraform {\n  required_providers {\n    local = {\n      source = "hashicorp/local"\n      version = "2.9.1"\n    }\n  }\n}\n\nvariable "secret" {\n  type = string\n}\n\nresource "terraform_data" "seed" {\n  input = "visible-675"\n  lifecycle {\n    action_trigger {\n      events = [after_create]\n      actions = [action.local_command.capture]\n    }\n  }\n}\n\naction "local_command" "capture" {\n  config {\n    command = "true"\n    arguments = ["visible-675", sensitive(var.secret), terraform_data.seed.id]\n    stdin = sensitive(var.secret)\n    environment = {\n      public = "visible-675"\n      private = sensitive(var.secret)\n      copy = var.secret\n    }\n  }\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","labels":<labels>,"vendorConfig":{"terraform":{"useSmartSanitization":<smart>}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_secret","type":"ENVIRONMENT_VARIABLE","value":"dummy-action-secret-675","writeOnly":true}' through A
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And the opened action plan of the run "run" uses "<mode>" and stays artifact-only
    And no object in the scenario's bucket holds "dummy-action-secret-675", of at least 3 objects
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/targeted-replan" with '{"targets":["terraform_data.seed"]}' through B
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And the run "run" recorded 2 worker plan artifacts
    And the opened action plan of the run "run" uses "<mode>" and stays artifact-only
    And no object in the scenario's bucket holds "dummy-action-secret-675", of at least 6 objects

    Examples:
      | mode | smart | labels |
      | DEFAULT | false | [] |
      | SMART | true | [] |
      | NONE | true | ["feature:disable_resource_sanitization"] |

  @terragrunt @op:runTrigger @case:success
  Scenario Outline: Terragrunt preserves selected native action units under <mode> with run-all <all>
    Given a Gitea repository "infra" with the files:
      | root.hcl | locals {\n  owner = "infraweave"\n}\n |
      | live/terragrunt.hcl | include "root" {\n  path = find_in_parent_folders("root.hcl")\n}\n |
      | live/main.tf | terraform {\n  required_providers {\n    local = {\n      source = "hashicorp/local"\n      version = "2.9.1"\n    }\n  }\n}\n\nvariable "secret" {\n  type = string\n}\n\nresource "terraform_data" "seed" {\n  input = "visible-675"\n  lifecycle {\n    action_trigger {\n      events = [after_create]\n      actions = [action.local_command.capture]\n    }\n  }\n}\n\naction "local_command" "capture" {\n  config {\n    command = "true"\n    arguments = ["visible-675", sensitive(var.secret), terraform_data.seed.id]\n    stdin = sensitive(var.secret)\n    environment = {\n      public = "visible-675"\n      private = sensitive(var.secret)\n      copy = var.secret\n    }\n  }\n}\n |
      | live/child/terragrunt.hcl | include "root" {\n  path = find_in_parent_folders("root.hcl")\n}\n |
      | live/child/main.tf | variable "secret" {\n  type = string\n}\nresource "terraform_data" "copy" {\n  input = var.secret\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","projectRoot":"live","space":"legacy","workerPool":"{pool}","labels":<labels>,"vendorConfig":{"terragrunt":{"tool":"TERRAFORM_FOSS","useRunAll":<all>,"useSmartSanitization":<smart>}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_secret","type":"ENVIRONMENT_VARIABLE","value":"dummy-action-secret-675","writeOnly":true}' through A
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And the opened Terragrunt action plan of the run "run" uses "<mode>" with units "<units>"
    And no object in the scenario's bucket holds "dummy-action-secret-675", of at least 2 objects

    Examples:
      | mode | all | smart | labels | units |
      | DEFAULT | true | false | [] | ., child |
      | SMART | true | true | [] | ., child |
      | SMART | false | true | [] | . |
      | NONE | true | true | ["feature:disable_resource_sanitization"] | ., child |

  @op:runTrigger @case:failure
  Scenario Outline: <mode> refuses an authored malformed action mask without a raw fallback
    Given a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "seed" {\n  input = "visible-675"\n}\n |
      | .infraweave/workflow.yml | init: terraform init\nworkspaceSelect: terraform workspace select ${{ workspaceName }}\nworkspaceNew: terraform workspace new ${{ workspaceName }}\nplan: terraform plan -out=${{ planFileName }}\nshowState: terraform show -json\nshowPlan: >-\n  printf '%s' '{"format_version":"1.2","terraform_version":"1.17.2","action_invocations":[{"address":"action.local_command.capture","config_values":{"stdin":"dummy-action-secret-675"},"config_sensitive":1}]}'\ngetOutputs: terraform output -json\napply: terraform apply ${{ planFileName }}\ndestroy: terraform destroy\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","labels":<labels>,"vendorConfig":{"terraform":{"useSmartSanitization":<smart>}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_secret","type":"ENVIRONMENT_VARIABLE","value":"dummy-action-secret-675","writeOnly":true}' through A
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FAILED" through A
    And the answer's last state transition's note is "The plan's plan.json could not be sealed: sanitize plan JSON: sanitize: malformed plan: invalid sensitivity marker."
    And the run "run" recorded 0 worker plan artifacts
    And token "h" reads the run "run"'s PLANNING log through A, which holds "terraform_data.seed" and not "dummy-action-secret-675"
    And no object in the scenario's bucket holds "dummy-action-secret-675", of at least 1 objects

    Examples:
      | mode | smart | labels |
      | DEFAULT | false | [] |
      | SMART | true | [] |
      | NONE | true | ["feature:disable_resource_sanitization"] |
