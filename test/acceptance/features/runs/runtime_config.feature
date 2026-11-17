@postgres @objectstore @vcs @docker
Feature: A run applies checked custom and repository runtime configuration
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
    And neither the answer nor a server's log names "runtime-alias-secret-566"
    When token "h" sends "GET /api/v1/stacks/infra/runs" through B
    Then the answer's items are the runs ""

    Examples:
      | configuration | pointer | reason |
      | {"yaml":"before_plan: [bad"} | /runtimeConfig/yaml | format |
      | {"yaml":"before_plan: \"x\""} | /runtimeConfig/yaml | format |
      | {"yaml":"befor_plan: []"} | /runtimeConfig/yaml | unknown-field |
      | {"yaml":"{environment: {TOKEN: &s runtime-alias-secret-566}, before_plan: [*s]}"} | /runtimeConfig/yaml | rule |
      | {"yaml":"{before_plan: [&s runtime-alias-secret-566], environment: {TOKEN: *s}}"} | /runtimeConfig/yaml | rule |
      | {"yaml":"{&s runtime-alias-secret-566: kept, environment: {TOKEN: *s}}"} | /runtimeConfig/yaml | rule |
      | {"yaml":"{&s runtime-alias-secret-566: kept, runtime-alias-secret-566: again, environment: {TOKEN: *s}}"} | /runtimeConfig/yaml | rule |
      | {"yaml":"{environment: {&env environment: baseline}, <<: { *env : {TOKEN: &s runtime-alias-secret-566}}, before_plan: [*s]}"} | /runtimeConfig/yaml | rule |
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


  @op:runTrigger @op:run @case:success
  Scenario Outline: Repository entries replace defaults while custom variables remain strongest
    Given a commit to "main" of "infra" writing ".infraweave/config.yml":
      """
      version: "2"
      stack_defaults:
        before_plan: ["echo hook: defaults"]
        environment: {LEVEL: defaults, FILE_SECRET: file-secret-567}
      stacks:
        infra:
          before_plan: ["echo hook: infra"]
      """
    And the head of "main" of "infra" is kept as "configured"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","projectRoot":"infra","vendorConfig":{"<vendor>":{}},"beforePlan":["echo hook: stack"],"afterPlan":["echo LEVEL=$LEVEL"]},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"LEVEL","type":"ENVIRONMENT_VARIABLE","value":"stack","writeOnly":false}' through A
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{configured}","runType":"PROPOSED"}' through B
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FINISHED" through A
    And the answer's repository runtime configuration includes '{"beforePlan":["echo hook: infra"],"environment":[{"key":"FILE_SECRET","value":""},{"key":"LEVEL","value":""}]}'
    And the answer's runtime configuration YAML is '{"before_plan":["echo hook: infra"]}'
    And neither the answer nor a server's log names "file-secret-567"
    And token "h" reads the run "run"'s PLANNING log through A, which holds "hook: infra" and not "hook: stack"
    And token "h" reads the run "run"'s PLANNING log through B, which holds "LEVEL=defaults" and not "file-secret-567"
    And token "h" records the run "run"'s PLANNING runtime evidence through A
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Other","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","projectRoot":"infra","vendorConfig":{"<vendor>":{}},"beforePlan":["echo hook: other"],"afterPlan":["echo LEVEL=$LEVEL"]},"manageState":true}' through A
    When token "h" sends "POST /api/v1/stacks/other/runs" with '{"commitSha":"{configured}","runType":"PROPOSED"}' through B
    Then it answers 200
    And the answer's id names "other"
    And token "h" eventually reads the run "other" in the state "FINISHED" through A
    And the answer's repository runtime configuration includes '{"beforePlan":["echo hook: defaults"]}'
    And the answer's runtime configuration YAML is '{"before_plan":["echo hook: defaults"],"environment":{"LEVEL":"","FILE_SECRET":""}}'
    And neither the answer nor a server's log names "file-secret-567"
    And token "h" reads the run "other"'s PLANNING log through B, which holds "hook: defaults" and not "hook: other"
    And token "h" records the run "other"'s PLANNING runtime evidence through A
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{configured}","runType":"PROPOSED","runtimeConfig":{"environment":[{"key":"LEVEL","value":"custom"}]}}' through B
    Then it answers 200
    And the answer's id names "custom"
    And token "h" eventually reads the run "custom" in the state "FINISHED" through A
    And the answer's custom runtime configuration includes '{"environment":[{"key":"LEVEL","value":""}]}'
    And the answer's repository runtime configuration includes '{"beforePlan":["echo hook: infra"]}'
    And neither the answer nor a server's log names "file-secret-567"
    And token "h" reads the run "custom"'s PLANNING log through B, which holds "LEVEL=custom" and not "hook: stack"
    And token "h" records the run "custom"'s PLANNING runtime evidence through A

    @terraform
    Examples:
      | vendor    |
      | terraform |

    @tofu
    Examples:
      | vendor   |
      | opentofu |

  @op:runTrigger @op:run @case:success
  Scenario Outline: The ignore label retains stack settings despite a repository file
    Given a commit to "main" of "infra" writing ".infraweave/config.yml":
      """
      version: "2"
      stack_defaults: {before_plan: ["echo hook: defaults"]}
      stacks: {infra: {before_plan: ["echo hook: infra"]}}
      """
    And the head of "main" of "infra" is kept as "configured"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","projectRoot":"infra","labels":["feature:ignore_runtime_config"],"vendorConfig":{"<vendor>":{}},"beforePlan":["echo hook: stack"]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{configured}","runType":"PROPOSED"}' through A
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And token "h" reads the run "run"'s PLANNING log through A, which holds "hook: stack" and not "hook: infra"

    @terraform
    Examples:
      | vendor    |
      | terraform |

    @tofu
    Examples:
      | vendor   |
      | opentofu |

  @op:runTrigger @op:run @case:success
  Scenario Outline: An unknown repository key warns during initialization and remains visible
    Given a commit to "main" of "infra" writing ".infraweave/config.yml":
      """
      version: "2"
      stacks:
        infra:
          before_plan: ["echo hook: infra"]
          environment: {TOKEN: &secret file-unknown-secret-567}
          befor_plan: *secret
      """
    And the head of "main" of "infra" is kept as "configured"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","projectRoot":"infra","vendorConfig":{"<vendor>":{}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{configured}","runType":"PROPOSED"}' through A
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's repository runtime configuration includes '{"beforePlan":["echo hook: infra"]}'
    And the answer's runtime configuration YAML is '{"before_plan":["echo hook: infra"],"environment":{"TOKEN":""},"befor_plan":""}'
    And neither the answer nor a server's log names "file-unknown-secret-567"
    And token "h" reads the run "run"'s INITIALIZING log through A, which holds "infraweave: .infraweave/config.yml: unknown key stacks.infra.befor_plan" and not "hook: infra"

    @terraform
    Examples:
      | vendor    |
      | terraform |

    @tofu
    Examples:
      | vendor   |
      | opentofu |

  @op:runTrigger @op:run @case:denial
  Scenario Outline: A writer applies repository configuration without the custom configuration action
    Given a commit to "main" of "infra" writing ".infraweave/config.yml":
      """
      version: "2"
      stacks: {infra: {before_plan: ["echo hook: infra"]}}
      """
    And the head of "main" of "infra" is kept as "configured"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","projectRoot":"infra","vendorConfig":{"<vendor>":{}}},"manageState":true}' through B
    And the member key is exchanged through A for token "m"
    And the member key is space-writer on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{configured}","runType":"PROPOSED","runtimeConfig":{}}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_TRIGGER_WITH_CUSTOM_RUNTIME_CONFIG"'
    When token "m" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{configured}","runType":"PROPOSED"}' through B
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FINISHED" through A
    And token "h" reads the run "run"'s PLANNING log through B, which holds "hook: infra" and not "hook: defaults"

    @terraform
    Examples:
      | vendor    |
      | terraform |

    @tofu
    Examples:
      | vendor   |
      | opentofu |

  @op:runTrigger @op:run @case:failure
  Scenario Outline: A wrong typed repository key fails initialization before every hook
    Given a commit to "main" of "infra" writing ".infraweave/config.yml":
      """
      version: "2"
      stacks: {infra: {before_plan: "x"}}
      """
    And the head of "main" of "infra" is kept as "configured"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","projectRoot":"infra","vendorConfig":{"<vendor>":{}},"beforeInit":["echo hook: before"],"afterInit":["echo hook: after"],"afterRun":["echo hook: after_run"]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{configured}","runType":"PROPOSED"}' through A
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, FAILED"
    And the answer's last state transition's note contains 'stacks.infra.before_plan'
    And token "h" records the run "run"'s repository refusal evidence through A

    @terraform
    Examples:
      | vendor    |
      | terraform |

    @tofu
    Examples:
      | vendor   |
      | opentofu |

  @runtime-config-reject @op:runTrigger @op:run @case:failure
  Scenario Outline: The reject server policy refuses an unknown repository key before hooks
    Given a commit to "main" of "infra" writing ".infraweave/config.yml":
      """
      version: "2"
      stacks: {infra: {befor_plan: ["typo"]}}
      """
    And the head of "main" of "infra" is kept as "configured"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","projectRoot":"infra","vendorConfig":{"<vendor>":{}},"beforeInit":["echo hook: before"],"afterRun":["echo hook: after_run"]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{configured}","runType":"PROPOSED"}' through A
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, FAILED"
    And the answer's last state transition's note contains 'stacks.infra.befor_plan'
    And token "h" records the run "run"'s repository refusal evidence through A

    @terraform
    Examples:
      | vendor    |
      | terraform |

    @tofu
    Examples:
      | vendor   |
      | opentofu |
