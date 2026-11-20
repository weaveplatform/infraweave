@terraform-plans @postgres @objectstore @vcs @docker @terraform @tofu @terragrunt @policy-plan-worker
Feature: Plan policies consume native sanitized documents and fresh replans
  Embedded OPA evaluates the documents uploaded by actual Terraform family
  workers. Allow continues in the planning container; warning pauses tracked
  autodeploy; a targeted replan cannot reuse an earlier decision or document.
  ref: decision 0008
  ref: decision 0009

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And token "h" eventually reads the OpenTofu versions through B as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  @case:success
  Scenario Outline: <engine> validates an actual sanitized <vendor> plan and applies in the same container
    Given a Gitea repository "infra" with the files:
      | main.tf | variable "secret" {\n  type = string\n  sensitive = true\n}\nresource "terraform_data" "this" {\n  input = var.secret\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"beforePlan":["hostname > /tmp/plan-container-id"],"beforeApply":["test \"$(cat /tmp/plan-container-id)\" = \"$(hostname)\""],"vendorConfig":{"<vendor>":{"useSmartSanitization":true}}},"manageState":true}' through B
    And it answers 201
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_secret","type":"ENVIRONMENT_VARIABLE","value":"native-secret-651","writeOnly":true}' through A
    And it answers 201
    And token "h" sends "POST /api/v1/policies" with '{"name":"Validate native document","type":"PLAN","space":"legacy","engineType":"<engine>","body":"<body>"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, APPLYING, FINISHED"
    And the answer's flags is '["native-verified"]'
    And the answer's plan policy outcomes are '[{"deny":[],"warn":[]}]'
    And no object in the scenario's bucket holds "native-secret-651", of at least 3 objects
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through A
    When token "h" sends "PATCH /api/v1/policies/{policy}" with '{"engineType":"REGO_V1","body":"package infraweave\nwarn contains \"Review no changes\" if true\nflag contains \"no-change-warned\" if true\n"}' through A
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through B
    Then it answers 200
    And the answer's id names "unchanged"
    And token "h" eventually reads the run "unchanged" in the state "FINISHED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FINISHED"
    And the answer's flags is '["no-change-warned"]'
    And the answer's plan policy outcomes are '[{"deny":[],"warn":["Review no changes"]}]'
    And the answer's PLAN receipt outcomes are "ALLOW"

    Examples:
      | vendor    | engine  | body |
      | terraform | REGO_V0 | package infraweave\\nvalid { input.terraform.resource_changes[0].change.after.input == sanitized(\\"native-secret-651\\"); count(input.terraform.terraform_version) > 0 }\\ndeny[\\"Unexpected sanitized native plan\\"] { not valid }\\nflag[\\"native-verified\\"] { valid }\\n |
      | opentofu  | REGO_V1 | package infraweave\\nvalid if { input.opentofu.resource_changes[0].change.after.input == sanitized(\\"native-secret-651\\"); input.terraform.resource_changes == input.opentofu.resource_changes; count(input.opentofu.opentofu_version) > 0 }\\ndeny contains \\"Unexpected sanitized native plan\\" if { not valid }\\nflag contains \\"native-verified\\" if valid\\n |

  @case:success
  Scenario Outline: Terragrunt evaluates exactly the selected sanitized units with run-all <all>
    Given a Gitea repository "infra" with the files:
      | root.hcl | locals {\n  owner = "infraweave"\n}\n |
      | live/terragrunt.hcl | include "root" {\n  path = find_in_parent_folders("root.hcl")\n}\n |
      | live/main.tf | variable "secret" {\n  type = string\n  sensitive = true\n}\nresource "terraform_data" "this" {\n  input = var.secret\n}\n |
      | live/child/terragrunt.hcl | include "root" {\n  path = find_in_parent_folders("root.hcl")\n}\n |
      | live/child/main.tf | variable "secret" {\n  type = string\n  sensitive = true\n}\nresource "terraform_data" "child" {\n  input = var.secret\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","projectRoot":"live","space":"legacy","workerPool":"{pool}","autodeploy":true,"beforePlan":["hostname > /tmp/plan-container-id"],"beforeApply":["test \"$(cat /tmp/plan-container-id)\" = \"$(hostname)\""],"vendorConfig":{"terragrunt":{"tool":"<tool>","useRunAll":<all>,"useSmartSanitization":true}}},"manageState":true}' through B
    And it answers 201
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_secret","type":"ENVIRONMENT_VARIABLE","value":"native-secret-651","writeOnly":true}' through A
    And it answers 201
    And token "h" sends "POST /api/v1/policies" with '{"name":"Validate native units","type":"PLAN","space":"legacy","body":"package infraweave\nvalid if { count(input.terragrunt) == <units>; every _, unit in input.terragrunt { count(unit.terraform_version) > 0; unit.resource_changes[0].change.after.input == sanitized(\"native-secret-651\") } }\ndeny contains \"Unexpected selected unit document\" if { not valid }\nflag contains \"units-verified\" if valid\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, APPLYING, FINISHED"
    And the answer's flags is '["units-verified"]'
    And the answer's plan policy outcomes are '[{"deny":[],"warn":[]}]'
    And no object in the scenario's bucket holds "native-secret-651", of at least 2 objects

    Examples:
      | tool           | all   | units |
      | TERRAFORM_FOSS | false | 1     |
      | OPEN_TOFU      | true  | 2     |

  @op:runTargetedReplan @case:success
  Scenario Outline: A warning holds <vendor> autodeploy and its targeted replan reevaluates the new document
    Given a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = "first"\n}\nresource "terraform_data" "other" {\n  input = "second"\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"<vendor>":{}}},"manageState":true}' through B
    And it answers 201
    And token "h" sends "POST /api/v1/policies" with '{"name":"Fresh targeted input","type":"PLAN","space":"legacy","body":"package infraweave\nwarn contains \"Review full plan\" if { count(input.terraform.resource_changes) == 2 }\ndeny contains \"Targeted change requires review\" if { count(input.terraform.resource_changes) == 1 }\nflag contains \"full-plan\" if { count(warn) > 0 }\nflag contains \"targeted-plan\" if { count(deny) > 0 }\nflag contains \"earlier-plan-flag\" if { count(deny) > 0; \"full-plan\" in input.infraweave.run.flags }\nflag contains \"previous-run-flag\" if { \"targeted-plan\" in input.infraweave.previous_run.flags }\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "UNCONFIRMED" through B
    And the answer's flags is '["full-plan"]'
    And the answer's plan policy outcomes are '[{"deny":[],"warn":["Review full plan"]}]'
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/targeted-replan" with '{"targets":["terraform_data.this"]}' through A
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, REPLAN_REQUESTED, PREPARING_REPLAN, PLANNING, FAILED"
    And the answer's last state transition's note is "denied by plan policy"
    And the answer's flags is '["earlier-plan-flag","full-plan","targeted-plan"]'
    And the answer's plan policy outcomes are '[{"deny":[],"warn":["Review full plan"]},{"deny":["Targeted change requires review"],"warn":[]}]'
    And the run "run" recorded 2 worker plan artifacts
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through A
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"PROPOSED"}' through A
    Then it answers 200
    And the answer's id names "next"
    And token "h" eventually reads the run "next" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FINISHED"
    And the answer's flags is '["full-plan","previous-run-flag"]'
    And the answer's plan policy outcomes are '[{"deny":[],"warn":["Review full plan"]}]'

    Examples:
      | vendor    |
      | terraform |
      | opentofu  |
