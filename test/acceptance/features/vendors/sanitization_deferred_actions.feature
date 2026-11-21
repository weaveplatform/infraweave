@terraform-plans @plan-sanitization @postgres @objectstore @vcs @docker @terraform
Feature: Captured experimental deferred-action document compatibility at the real upload boundary
  The unchanged full JSON comes from the approved source-pinned, locally patched
  development producer. A stable worker transports it through a custom show
  command. This proves artifact compatibility, not official-release production
  or experimental action execution.
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

  @op:runTrigger @case:success
  Scenario Outline: <mode> transforms the captured development deferred-action document before recording
    Given a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "seed" {\n  input = 1\n}\n |
      | compatibility-plan.json | {"format_version":"1.2","terraform_version":"1.17.2-dev","variables":{"secret":{"value":"dummy-action-secret-675"}},"planned_values":{"root_module":{"resources":[{"address":"terraform_data.seed","mode":"managed","type":"terraform_data","name":"seed","provider_name":"terraform.io/builtin/terraform","schema_version":0,"values":{"input":1,"store":null,"triggers_replace":null},"sensitive_values":{}}]}},"resource_changes":[{"address":"terraform_data.seed","mode":"managed","type":"terraform_data","name":"seed","provider_name":"terraform.io/builtin/terraform","change":{"actions":["create"],"before":null,"after":{"input":1,"store":null,"triggers_replace":null},"after_unknown":{"id":true,"output":true},"before_sensitive":false,"after_sensitive":{}}}],"deferred_changes":[{"reason":"instance_count_unknown","resource_change":{"address":"terraform_data.deferred[*]","mode":"managed","type":"terraform_data","name":"deferred","index_unknown":true,"provider_name":"terraform.io/builtin/terraform","change":{"actions":["create"],"before":null,"after":{"input":{"copy":"dummy-action-secret-675","private":"dummy-action-secret-675","public":"visible-675"},"store":null,"triggers_replace":null},"after_unknown":{"id":true,"input":{},"output":true},"before_sensitive":false,"after_sensitive":{"input":{"private":true},"output":{}}}}},{"reason":"deferred_prereq","resource_change":{"address":"terraform_data.trigger","mode":"managed","type":"terraform_data","name":"trigger","provider_name":"terraform.io/builtin/terraform","change":{"actions":["create"],"before":null,"after":{"input":"visible-675","store":null,"triggers_replace":null},"after_unknown":{"id":true,"output":true},"before_sensitive":false,"after_sensitive":{}}}}],"deferred_action_invocations":[{"reason":"deferred_prereq","action_invocation":{"address":"action.local_command.capture","type":"local_command","name":"capture","provider_name":"registry.terraform.io/hashicorp/local","lifecycle_action_trigger":{"triggering_resource_address":"terraform_data.trigger","action_trigger_event":"AfterCreate","action_trigger_block_index":0,"actions_list_index":0,"on_failure":"ActionOnFailureHalt"}}}],"configuration":{"provider_config":{"local":{"name":"local","full_name":"registry.terraform.io/hashicorp/local","version_constraint":"2.9.1"},"terraform":{"name":"terraform","full_name":"terraform.io/builtin/terraform"}},"root_module":{"resources":[{"address":"terraform_data.deferred","mode":"managed","type":"terraform_data","name":"deferred","provider_config_key":"terraform","expressions":{"input":{"references":["var.secret"]}},"schema_version":0,"count_expression":{"references":["terraform_data.seed.output","terraform_data.seed"]}},{"address":"terraform_data.seed","mode":"managed","type":"terraform_data","name":"seed","provider_config_key":"terraform","expressions":{"input":{"constant_value":1}},"schema_version":0},{"address":"terraform_data.trigger","mode":"managed","type":"terraform_data","name":"trigger","provider_config_key":"terraform","expressions":{"input":{"constant_value":"visible-675"}},"schema_version":0}],"variables":{"secret":{"sensitive":true}},"actions":[{"address":"action.local_command.capture","type":"local_command","name":"capture","provider_config_key":"local"}]}},"timestamp":"2026-11-21T05:13:34Z","applyable":true,"complete":false,"errored":false}\n |
      | .infraweave/workflow.yml | init: terraform init\nworkspaceSelect: terraform workspace select ${{ workspaceName }}\nworkspaceNew: terraform workspace new ${{ workspaceName }}\nplan: terraform plan -out=${{ planFileName }}\nshowState: terraform show -json\nshowPlan: cat compatibility-plan.json\ngetOutputs: terraform output -json\napply: terraform apply ${{ planFileName }}\ndestroy: terraform destroy\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","labels":<labels>,"vendorConfig":{"terraform":{"useSmartSanitization":<smart>}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_secret","type":"ENVIRONMENT_VARIABLE","value":"dummy-action-secret-675","writeOnly":true}' through A
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And the opened development deferred-action compatibility plan of the run "run" uses "<mode>" and stays artifact-only
    And no object in the scenario's bucket holds "dummy-action-secret-675", of at least 3 objects

    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/targeted-replan" with '{"targets":["terraform_data.seed"]}' through B
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And the run "run" recorded 2 worker plan artifacts
    And the opened development deferred-action compatibility plan of the run "run" uses "<mode>" and stays artifact-only

    Examples:
      | mode | smart | labels |
      | DEFAULT | false | [] |
      | SMART | true | [] |
      | NONE | true | ["feature:disable_resource_sanitization"] |

  @terragrunt @op:runTrigger @case:success
  Scenario Outline: Terragrunt transports the captured deferred action with selected units under <mode> and run-all <all>
    Given a Gitea repository "infra" with the files:
      | root.hcl | locals {\n  owner = "infraweave"\n}\n |
      | live/terragrunt.hcl | include "root" {\n  path = find_in_parent_folders("root.hcl")\n}\n |
      | live/main.tf | resource "terraform_data" "seed" {\n  input = "visible-675"\n}\n |
      | live/child/terragrunt.hcl | include "root" {\n  path = find_in_parent_folders("root.hcl")\n}\n |
      | live/child/main.tf | variable "secret" {\n  type = string\n}\nresource "terraform_data" "copy" {\n  input = var.secret\n}\n |
      | live/compatibility-plan.json | {"format_version":"1.2","terraform_version":"1.17.2-dev","variables":{"secret":{"value":"dummy-action-secret-675"}},"planned_values":{"root_module":{"resources":[{"address":"terraform_data.seed","mode":"managed","type":"terraform_data","name":"seed","provider_name":"terraform.io/builtin/terraform","schema_version":0,"values":{"input":1,"store":null,"triggers_replace":null},"sensitive_values":{}}]}},"resource_changes":[{"address":"terraform_data.seed","mode":"managed","type":"terraform_data","name":"seed","provider_name":"terraform.io/builtin/terraform","change":{"actions":["create"],"before":null,"after":{"input":1,"store":null,"triggers_replace":null},"after_unknown":{"id":true,"output":true},"before_sensitive":false,"after_sensitive":{}}}],"deferred_changes":[{"reason":"instance_count_unknown","resource_change":{"address":"terraform_data.deferred[*]","mode":"managed","type":"terraform_data","name":"deferred","index_unknown":true,"provider_name":"terraform.io/builtin/terraform","change":{"actions":["create"],"before":null,"after":{"input":{"copy":"dummy-action-secret-675","private":"dummy-action-secret-675","public":"visible-675"},"store":null,"triggers_replace":null},"after_unknown":{"id":true,"input":{},"output":true},"before_sensitive":false,"after_sensitive":{"input":{"private":true},"output":{}}}}},{"reason":"deferred_prereq","resource_change":{"address":"terraform_data.trigger","mode":"managed","type":"terraform_data","name":"trigger","provider_name":"terraform.io/builtin/terraform","change":{"actions":["create"],"before":null,"after":{"input":"visible-675","store":null,"triggers_replace":null},"after_unknown":{"id":true,"output":true},"before_sensitive":false,"after_sensitive":{}}}}],"deferred_action_invocations":[{"reason":"deferred_prereq","action_invocation":{"address":"action.local_command.capture","type":"local_command","name":"capture","provider_name":"registry.terraform.io/hashicorp/local","lifecycle_action_trigger":{"triggering_resource_address":"terraform_data.trigger","action_trigger_event":"AfterCreate","action_trigger_block_index":0,"actions_list_index":0,"on_failure":"ActionOnFailureHalt"}}}],"configuration":{"provider_config":{"local":{"name":"local","full_name":"registry.terraform.io/hashicorp/local","version_constraint":"2.9.1"},"terraform":{"name":"terraform","full_name":"terraform.io/builtin/terraform"}},"root_module":{"resources":[{"address":"terraform_data.deferred","mode":"managed","type":"terraform_data","name":"deferred","provider_config_key":"terraform","expressions":{"input":{"references":["var.secret"]}},"schema_version":0,"count_expression":{"references":["terraform_data.seed.output","terraform_data.seed"]}},{"address":"terraform_data.seed","mode":"managed","type":"terraform_data","name":"seed","provider_config_key":"terraform","expressions":{"input":{"constant_value":1}},"schema_version":0},{"address":"terraform_data.trigger","mode":"managed","type":"terraform_data","name":"trigger","provider_config_key":"terraform","expressions":{"input":{"constant_value":"visible-675"}},"schema_version":0}],"variables":{"secret":{"sensitive":true}},"actions":[{"address":"action.local_command.capture","type":"local_command","name":"capture","provider_config_key":"local"}]}},"timestamp":"2026-11-21T05:13:34Z","applyable":true,"complete":false,"errored":false}\n |
      | live/.infraweave/workflow.yml | init: terragrunt run <scope> -- init -input=false\nworkspaceSelect: terragrunt run <scope> -- workspace select ${{ workspaceName }}\nworkspaceNew: terragrunt run <scope> -- workspace new ${{ workspaceName }}\nplan: >-\n  terragrunt run <scope> -- plan -input=false -out=${{ planFileName }} && mkdir -p "$TG_JSON_OUT_DIR" && cp compatibility-plan.json "$TG_JSON_OUT_DIR/tfplan.json"\nshowState: terragrunt run -- show -json\nshowPlan: cat compatibility-plan.json\ngetOutputs: terragrunt run -- output -json\napply: terragrunt run <scope> -- apply -input=false\ndestroy: terragrunt run <scope> -- destroy -input=false\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","projectRoot":"live","space":"legacy","workerPool":"{pool}","labels":<labels>,"vendorConfig":{"terragrunt":{"tool":"TERRAFORM_FOSS","useRunAll":<all>,"useSmartSanitization":<smart>}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_secret","type":"ENVIRONMENT_VARIABLE","value":"dummy-action-secret-675","writeOnly":true}' through A
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And the opened Terragrunt development deferred-action plan of the run "run" uses "<mode>" with units "<units>"
    And no object in the scenario's bucket holds "dummy-action-secret-675", of at least 2 objects

    Examples:
      | mode | all | scope | smart | labels | units |
      | DEFAULT | true | --all | false | [] | ., child |
      | SMART | true | --all | true | [] | ., child |
      | SMART | false |  | true | [] | . |
      | NONE | true | --all | true | ["feature:disable_resource_sanitization"] | ., child |
