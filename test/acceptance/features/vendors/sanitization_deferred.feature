@terraform-plans @plan-sanitization @postgres @objectstore @vcs @docker @terraform
Feature: Captured deferred-resource document compatibility at the real upload boundary
  The JSON is the unchanged tool-produced experimental fixture, injected via a
  custom show command into the actual stable worker. This proves artifact
  compatibility, and does not admit alpha releases or test deferred execution.
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
  Scenario Outline: <mode> transforms the captured deferred-resource document before recording
    Given a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "seed" {\n  input = 1\n}\n |
      | compatibility-plan.json | {"format_version":"1.2","terraform_version":"1.17.0-alpha20260827","variables":{"secret":{"value":"dummy-action-secret-675"}},"planned_values":{"root_module":{"resources":[{"address":"terraform_data.seed","mode":"managed","type":"terraform_data","name":"seed","provider_name":"terraform.io/builtin/terraform","schema_version":0,"values":{"input":1,"store":null,"triggers_replace":null},"sensitive_values":{}}]}},"resource_changes":[{"address":"terraform_data.seed","mode":"managed","type":"terraform_data","name":"seed","provider_name":"terraform.io/builtin/terraform","change":{"actions":["create"],"before":null,"after":{"input":1,"store":null,"triggers_replace":null},"after_unknown":{"id":true,"output":true},"before_sensitive":false,"after_sensitive":{}}}],"deferred_changes":[{"reason":"instance_count_unknown","resource_change":{"address":"terraform_data.deferred[*]","mode":"managed","type":"terraform_data","name":"deferred","index_unknown":true,"provider_name":"terraform.io/builtin/terraform","change":{"actions":["create"],"before":null,"after":{"input":{"copy":"dummy-action-secret-675","private":"dummy-action-secret-675","public":"visible-675"},"store":null,"triggers_replace":null},"after_unknown":{"id":true,"input":{},"output":true},"before_sensitive":false,"after_sensitive":{"input":{"private":true},"output":{}}}}}],"configuration":{"provider_config":{"local":{"name":"local","full_name":"registry.terraform.io/hashicorp/local","version_constraint":"2.9.1"},"terraform":{"name":"terraform","full_name":"terraform.io/builtin/terraform"}},"root_module":{"resources":[{"address":"terraform_data.deferred","mode":"managed","type":"terraform_data","name":"deferred","provider_config_key":"terraform","expressions":{"input":{"references":["var.secret"]}},"schema_version":0,"count_expression":{"references":["terraform_data.seed.output","terraform_data.seed"]}},{"address":"terraform_data.seed","mode":"managed","type":"terraform_data","name":"seed","provider_config_key":"terraform","expressions":{"input":{"constant_value":1}},"schema_version":0}],"variables":{"secret":{"sensitive":true}},"actions":[{"address":"action.local_command.capture","type":"local_command","name":"capture","provider_config_key":"local"}]}},"timestamp":"2026-11-20T01:58:28Z","applyable":true,"complete":false,"errored":false}\n |
      | .infraweave/workflow.yml | init: terraform init\nworkspaceSelect: terraform workspace select ${{ workspaceName }}\nworkspaceNew: terraform workspace new ${{ workspaceName }}\nplan: terraform plan -out=${{ planFileName }}\nshowState: terraform show -json\nshowPlan: cat compatibility-plan.json\ngetOutputs: terraform output -json\napply: terraform apply ${{ planFileName }}\ndestroy: terraform destroy\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","labels":<labels>,"vendorConfig":{"terraform":{"useSmartSanitization":<smart>}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_secret","type":"ENVIRONMENT_VARIABLE","value":"dummy-action-secret-675","writeOnly":true}' through A
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And the opened deferred-resource compatibility plan of the run "run" uses "<mode>" and stays artifact-only
    And no object in the scenario's bucket holds "dummy-action-secret-675", of at least 3 objects

    Examples:
      | mode | smart | labels |
      | DEFAULT | false | [] |
      | SMART | true | [] |
      | NONE | true | ["feature:disable_resource_sanitization"] |
