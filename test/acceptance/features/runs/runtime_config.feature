@postgres @objectstore @vcs @docker
Feature: A trigger checks and applies its custom runtime configuration
  A custom configuration requires the admin action, replaces the stack's
  own supplied settings, and keeps context hooks around the replacement.
  Its variable values reach the runner but never a run read. Sparse paths
  narrow the Git checkout. YAML uses one checked stack-settings document.
  Real private-pool runners exercise Terraform and OpenTofu, with release
  constraints resolved from the recorded catalogue and verified downloads.
  ref: decision 0007
  ref: decision 0008
  ref: decision 0013
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And token "h" eventually reads the OpenTofu versions through B as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | infra/main.tf  | resource "terraform_data" "this" {\n  input = "runtime"\n} |
      | docs/notes.txt | documentation |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  @op:runTrigger @op:run @case:success
  Scenario Outline: Fields replace stack hooks and variables while context hooks wrap a sparse checkout
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","projectRoot":".","autodeploy":true,"vendorConfig":{"<vendor>":{}},"beforePlan":["echo hook: stack"]},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"LEVEL","type":"ENVIRONMENT_VARIABLE","value":"stack","writeOnly":false}' through A
    And token "h" sends "POST /api/v1/contexts" with '{"name":"Shared","space":"legacy","hooks":{"afterApply":[],"afterDestroy":[],"afterInit":[],"afterPerform":[],"afterPlan":[],"afterRun":[],"beforeApply":[],"beforeDestroy":[],"beforeInit":[],"beforePerform":[],"beforePlan":["echo hook: context"]},"configAttachments":[{"id":"LEVEL","type":"ENVIRONMENT_VARIABLE","value":"context","writeOnly":false}]}' through B
    Then it answers 201
    And token "h" sends "PUT /api/v1/stacks/infra/contexts/shared" with '{"priority":0}' through A
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"TRACKED","runtimeConfig":{"beforePlan":["echo hook: custom","echo \"LEVEL=$LEVEL\"","if test -f ../docs/notes.txt; then echo docs: present; else echo docs: absent; fi"],"environment":[{"key":"LEVEL","value":"custom"},{"key":"RUNTIME_SECRET","value":"runtime-secret-566"}],"projectRoot":"infra","gitSparseCheckoutPaths":["infra"]}}' through B
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FINISHED" through A
    And the answer's custom runtime configuration includes '{"beforePlan":["echo hook: custom","echo \"LEVEL=$LEVEL\"","if test -f ../docs/notes.txt; then echo docs: present; else echo docs: absent; fi"],"environment":[{"key":"LEVEL","value":""},{"key":"RUNTIME_SECRET","value":""}],"projectRoot":"infra","gitSparseCheckoutPaths":["infra"]}'
    And the answer's runtime configuration includes '{"beforePlan":["echo hook: context","echo hook: custom","echo \"LEVEL=$LEVEL\"","if test -f ../docs/notes.txt; then echo docs: present; else echo docs: absent; fi"],"environment":[{"key":"LEVEL","value":""},{"key":"RUNTIME_SECRET","value":""}],"projectRoot":"infra","gitSparseCheckoutPaths":["infra"]}'
    And neither the answer nor a server's log names "runtime-secret-566"
    And token "h" reads the run "run"'s PLANNING log through A, which holds "hook: context\nhook: custom\nLEVEL=custom\ndocs: absent" and not "hook: stack"
    And token "h" records the run "run"'s PLANNING runtime evidence through B

    @terraform
    Examples:
      | vendor    |
      | terraform |

    @tofu
    Examples:
      | vendor   |
      | opentofu |

  @op:runTrigger @op:run @case:success
  Scenario Outline: YAML runs the requested older catalogue release and redacts its variables
    Given the fixture's previous <tool> release is kept as "older"
    And the tool mirror serves the pinned previous <tool> release kept as "older"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","projectRoot":"infra","vendorConfig":{"<vendor>":{}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"PROPOSED","runtimeConfig":{"yaml":"before_plan:\n  - \"echo hook: yaml\"\n  - <binary> version\nenvironment:\n  RUNTIME_SECRET: runtime-yaml-secret-566\n<settings>\n"}}' through B
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FINISHED" through A
    And the answer's custom runtime configuration includes '{"beforePlan":["echo hook: yaml","<binary> version"],"environment":[{"key":"RUNTIME_SECRET","value":""}]}'
    And neither the answer nor a server's log names "runtime-yaml-secret-566"
    And token "h" reads the run "run"'s PLANNING log through B, which holds "hook: yaml" and not "runtime-yaml-secret-566"
    And token "h" reads the run "run"'s PLANNING log through A, which holds "<tool> v{older}" and not "runtime-yaml-secret-566"
    And token "h" records the run "run"'s PLANNING runtime evidence through B

    @terraform
    Examples:
      | tool      | vendor    | binary    | settings                    |
      | Terraform | terraform | terraform | terraform_version: {older} |

    @tofu
    Examples:
      | tool     | vendor   | binary | settings                         |
      | OpenTofu | opentofu | tofu   | opentofu: {version: {older}} |

  @terraform @op:runTrigger @case:denial
  Scenario: A writer triggers plain runs and an admin triggers custom runs
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","projectRoot":"infra","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And the member key is exchanged through A for token "m"
    And the member key is space-writer on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"TRACKED","runtimeConfig":{}}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_TRIGGER_WITH_CUSTOM_RUNTIME_CONFIG"'
    When token "m" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through B
    Then it answers 200
    And the answer's id names "plain"
    Given the member key is space-admin on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"TRACKED","runtimeConfig":{}}' through A
    Then it answers 200
    And the answer's id names "first"
    When token "m" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"TRACKED","runtimeConfig":{}}' through B
    Then it answers 200
    And the answer's id names "second"
    When token "h" sends "GET /api/v1/stacks/infra/runs" through A
    Then the answer's items are the runs "second, first, plain"

  @terraform @op:runTrigger @case:failure
  Scenario Outline: Invalid custom input creates no run
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","projectRoot":"infra","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runtimeConfig":<configuration>}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "<pointer>" as "<reason>"
    When token "h" sends "GET /api/v1/stacks/infra/runs" through B
    Then the answer's items are the runs ""

    Examples:
      | configuration | pointer | reason |
      | {"yaml":"before_plan: [bad"} | /runtimeConfig/yaml | format |
      | {"yaml":"before_plan: \"x\""} | /runtimeConfig/yaml | format |
      | {"yaml":"befor_plan: []"} | /runtimeConfig/yaml | unknown-field |
      | {"beforePlan":"x"} | /runtimeConfig/beforePlan | type |
      | {"beforPlan":[]} | /runtimeConfig/beforPlan | unknown-field |
      | {"beforPlan":null} | /runtimeConfig/beforPlan | unknown-field |
      | {"environment":[{"key":"INFRAWEAVE_API_TOKEN","value":"bad"}]} | /runtimeConfig/environment/0/key | rule |
      | {"projectRoot":"../outside"} | /runtimeConfig/projectRoot | rule |
      | {"yaml":"terragrunt: {use_run_all: false}"} | /runtimeConfig/yaml | rule |

  @terraform @op:runTrigger @op:run @case:failure
  Scenario: An unmatched runtime release fails preparation naming the custom setting
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","projectRoot":"infra","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runtimeConfig":{"yaml":"terraform_version: \"> 9999.0.0\""}}' through A
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note contains '/runtimeConfig/yaml'
    And the answer has no recorded runtime configuration
    And the answer's custom runtime configuration includes '{"terraformVersion":"> 9999.0.0"}'
