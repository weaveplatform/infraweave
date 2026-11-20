@plan-sanitization @postgres @objectstore @vcs @docker @terraform
Feature: Unsupported full-plan value families fail closed before upload
  These authored documents follow released tool field shapes and are injected
  through a custom show command into the actual worker upload path. They are
  not tool-produced fixtures or upstream captures. Complete family support and
  real tool fixture acquisition remain in POL-FOLLOWUP-PLAN-PAYLOADS (#675).
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


  @op:runTrigger @case:failure
  Scenario Outline: <mode> refuses nonempty <family> without recording any raw plan
    Given a Gitea repository "infra" with the files:
      | main.tf | variable "secret" {\n  type = string\n  sensitive = true\n}\nresource "terraform_data" "secret" {\n  input = { private = var.secret, public = "visible-642" }\n}\n |
      | .infraweave/workflow.yml | init: terraform init\nworkspaceSelect: terraform workspace select ${{ workspaceName }}\nworkspaceNew: terraform workspace new ${{ workspaceName }}\nplan: terraform plan -out=${{ planFileName }}\nshowState: terraform show -json\nshowPlan: >-\n  printf '%s' '{"format_version":"1.2","terraform_version":"1.17.2","resource_changes":[{"address":"terraform_data.secret","mode":"managed","type":"terraform_data","name":"secret","change":{"actions":["create"],"after":{"input":{"private":"dummy-plan-secret-642","public":"visible-642"}},"after_sensitive":{"input":{"private":true}}}}],"<family>":<payload>}'\ngetOutputs: terraform output -json\napply: terraform apply ${{ planFileName }}\ndestroy: terraform destroy\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","labels":<labels>,"vendorConfig":{"terraform":{"useSmartSanitization":<smart>}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_secret","type":"ENVIRONMENT_VARIABLE","value":"dummy-plan-secret-642","writeOnly":true}' through A
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FAILED" through A
    And the answer's last state transition's note is "The plan's plan.json could not be sealed: sanitize plan JSON: sanitize: unsupported plan values: <family>."
    And the run "run" recorded 0 worker plan artifacts
    And token "h" reads the run "run"'s PLANNING log through A, which holds "terraform_data.secret" and not "dummy-plan-secret-642"
    And no object in the scenario's bucket holds "dummy-plan-secret-642", of at least 1 objects

    Examples:
      | mode | smart | labels | family | payload |
      | DEFAULT | false | [] | deferred_action_invocations | [{"reason":"resource_config_unknown","action_invocation":{"address":"action.example.send","type":"example","name":"send","config_values":{"token":"dummy-plan-secret-642"},"config_sensitive":{"token":true}}}] |
      | SMART | true | [] | deferred_action_invocations | [{"reason":"resource_config_unknown","action_invocation":{"address":"action.example.send","type":"example","name":"send","config_values":{"token":"dummy-plan-secret-642"},"config_sensitive":{"token":true}}}] |

  @op:runTrigger @case:success
  Scenario Outline: NONE remains the explicit sealed opt-out for <family>
    Given a Gitea repository "infra" with the files:
      | main.tf | variable "secret" {\n  type = string\n  sensitive = true\n}\nresource "terraform_data" "secret" {\n  input = { private = var.secret, public = "visible-642" }\n}\n |
      | .infraweave/workflow.yml | init: terraform init\nworkspaceSelect: terraform workspace select ${{ workspaceName }}\nworkspaceNew: terraform workspace new ${{ workspaceName }}\nplan: terraform plan -out=${{ planFileName }}\nshowState: terraform show -json\nshowPlan: >-\n  printf '%s' '{"format_version":"1.2","terraform_version":"1.17.2","resource_changes":[{"address":"terraform_data.secret","mode":"managed","type":"terraform_data","name":"secret","change":{"actions":["create"],"after":{"input":{"private":"dummy-plan-secret-642","public":"visible-642"}},"after_sensitive":{"input":{"private":true}}}}],"<family>":<payload>}'\ngetOutputs: terraform output -json\napply: terraform apply ${{ planFileName }}\ndestroy: terraform destroy\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","labels":<labels>,"vendorConfig":{"terraform":{"useSmartSanitization":<smart>}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_secret","type":"ENVIRONMENT_VARIABLE","value":"dummy-plan-secret-642","writeOnly":true}' through A
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And the opened worker plan of the run "run" and its "terraform" policy projection use "NONE" with units ""
    And the opened worker plan of the run "run" retains "<family>" as '<payload>'
    And no object in the scenario's bucket holds "dummy-plan-secret-642", of at least 3 objects

    Examples:
      | smart | labels | family | payload |
      | true | ["feature:disable_resource_sanitization"] | deferred_changes | [{"reason":"instance_count_unknown","resource_change":{"address":"terraform_data.hidden","mode":"managed","type":"terraform_data","name":"hidden","change":{"actions":["create"],"after":{"input":"dummy-plan-secret-642"},"after_sensitive":{"input":true}}}}] |
      | true | ["feature:disable_resource_sanitization"] | action_invocations | [{"address":"action.example.send","type":"example","name":"send","config_values":{"token":"dummy-plan-secret-642"},"config_sensitive":{"token":true}}] |
      | true | ["feature:disable_resource_sanitization"] | deferred_action_invocations | [{"reason":"resource_config_unknown","action_invocation":{"address":"action.example.send","type":"example","name":"send","config_values":{"token":"dummy-plan-secret-642"},"config_sensitive":{"token":true}}}] |
