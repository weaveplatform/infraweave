@postgres
Feature: Terragrunt stacks
  A stack whose vendorConfig sets terragrunt runs Terragrunt, which drives
  OpenTofu or Terraform as its tool setting chooses, TERRAFORM_FOSS when it
  names none. It reads back as Terragrunt, with effectiveVersions the
  releases its runs use: the newest stable Terragrunt release the version
  catalogue lists that terragruntVersion allows, and the newest release of
  the tool that the stack's terraformVersion, else the Terragrunt
  settings' terraformVersion, allows, each the newest listed when no
  constraint is set and null when none listed meets it. Whether
  infraweave keeps its state is decided when it is created: by
  useStateManagement when it is set, by the create's manageState when it
  is null, and an update leaves it as it was. A manually provisioned tool,
  smart sanitization, reusing the saved plan and prefixing resource names
  ask for what infraweave does not run yet, and answer 422 naming the
  setting. The vendor-terragrunt option gates every request that sets the
  member. A queued run's specification names Terragrunt and the tool it
  drives, each at its resolved release, and the state backend only when
  infraweave keeps the stack's state; a constraint no listed release meets
  refuses it 422 naming the constraint. Such a run records no outputs, and
  its apply finishes leaving its stack none. The release indexes serve
  the recorded fixtures, Terragrunt's GitHub releases with its alpha, beta
  and release-candidate tags. The home key is space-admin on the
  account's top space; the member key holds only the bindings a scenario
  gives it.
  ref: decision 0008
  ref: decision 0010
  ref: decision 0016
  ref: decision 0021

  Background:
    Given the release indexes serve the recorded fixtures

  @op:stackCreate @op:stack @op:stackUpdate @op:stackPatch @op:stacks @case:success
  Scenario: A Terragrunt stack driving OpenTofu reads back as Terragrunt, with its effective versions and state management
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy","vendorConfig":{"terragrunt":{"tool":"OPEN_TOFU","terragruntVersion":"~> 0.73.0","terraformVersion":"~> 1.6.0","useRunAll":true,"useSmartSanitization":false,"useStateManagement":false}}},"manageState":true}' through A
    Then it answers 201
    And the answer's managesStateFile is 'false'
    And the answer's vendorConfig's kind is '"StackConfigVendorTerragrunt"'
    And the answer's vendorConfig's useStateManagement is 'false'
    And token "h" eventually reads the stack "infra" through B with the effective versions '{"tool":"OPEN_TOFU","effectiveTerragruntVersion":"0.73.16","effectiveTerraformVersion":"1.6.3"}'
    And the answer's vendorConfig is '{"kind":"StackConfigVendorTerragrunt","effectiveVersions":{"tool":"OPEN_TOFU","effectiveTerragruntVersion":"0.73.16","effectiveTerraformVersion":"1.6.3"},"tool":"OPEN_TOFU","terragruntVersion":"~> 0.73.0","terraformVersion":"~> 1.6.0","useRunAll":true,"useSmartSanitization":false,"useStateManagement":false,"skipReplan":false}'
    When token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"main","space":"legacy","vendorConfig":{"terragrunt":{"tool":"TERRAFORM_FOSS","terragruntVersion":"0.73.11","useRunAll":false,"useSmartSanitization":false,"useStateManagement":true}}}' through B
    Then it answers 200
    And the answer's managesStateFile is 'false'
    And the answer's vendorConfig's useStateManagement is 'false'
    And the answer's vendorConfig's effectiveVersions is '{"tool":"TERRAFORM_FOSS","effectiveTerragruntVersion":"0.73.11","effectiveTerraformVersion":"{terraform.newest}"}'
    When token "h" sends "PATCH /api/v1/stacks/infra" with '{"terraformVersion":"~> 1.5.0","vendorConfig":{"terragrunt":{"terragruntVersion":"~> 0.73.0"}}}' through A
    Then it answers 200
    And the answer's vendorConfig's tool is '"TERRAFORM_FOSS"'
    And the answer's vendorConfig's effectiveVersions is '{"tool":"TERRAFORM_FOSS","effectiveTerragruntVersion":"0.73.16","effectiveTerraformVersion":"1.5.7"}'
    When token "h" sends "GET /api/v1/stacks" through B
    Then the answer lists the stacks "infra"

  @op:stackCreate @case:success
  Scenario: A Terragrunt stack's useStateManagement decides whether infraweave keeps its state, and manageState when it is null
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Kept","repository":"infra","branch":"main","space":"legacy","vendorConfig":{"terragrunt":{"useRunAll":true,"useSmartSanitization":false}}},"manageState":true}' through A
    Then it answers 201
    And the answer's managesStateFile is 'true'
    And the answer's vendorConfig's useStateManagement is 'true'
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Unkept","repository":"infra","branch":"main","space":"legacy","vendorConfig":{"terragrunt":{"useRunAll":true,"useSmartSanitization":false}}},"manageState":false}' through B
    Then it answers 201
    And the answer's managesStateFile is 'false'
    And the answer's vendorConfig's useStateManagement is 'false'
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Forced","repository":"infra","branch":"main","space":"legacy","vendorConfig":{"terragrunt":{"useRunAll":true,"useSmartSanitization":false,"useStateManagement":true}}},"manageState":false}' through A
    Then it answers 201
    And the answer's managesStateFile is 'true'
    And the answer's vendorConfig's useStateManagement is 'true'

  @op:stackCreate @op:stackUpdate @op:stackPatch @case:failure
  Scenario: Each setting infraweave does not run yet answers 422 naming it
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Manual","repository":"infra","branch":"main","space":"legacy","vendorConfig":{"terragrunt":{"tool":"MANUALLY_PROVISIONED","useRunAll":true,"useSmartSanitization":false}}},"manageState":true}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/input/vendorConfig/terragrunt/tool" as "rule"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Later","repository":"infra","branch":"main","space":"legacy","vendorConfig":{"terragrunt":{"useRunAll":true,"useSmartSanitization":true,"skipReplan":true,"skipReplanWhenRunAll":true,"prefixResourceNamesWithModuleName":true,"terragruntVersion":"latest"}}},"manageState":true}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/input/vendorConfig/terragrunt/useSmartSanitization" as "rule"
    And the problem refuses the field "/input/vendorConfig/terragrunt/skipReplan" as "rule"
    And the problem refuses the field "/input/vendorConfig/terragrunt/skipReplanWhenRunAll" as "rule"
    And the problem refuses the field "/input/vendorConfig/terragrunt/prefixResourceNamesWithModuleName" as "rule"
    And the problem refuses the field "/input/vendorConfig/terragrunt/terragruntVersion" as "format"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy","vendorConfig":{"terragrunt":{"useRunAll":true,"useSmartSanitization":false}}},"manageState":true}' through A
    Then it answers 201
    When token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"main","space":"legacy","vendorConfig":{"terragrunt":{"useRunAll":true,"useSmartSanitization":false,"skipReplan":true}}}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/vendorConfig/terragrunt/skipReplan" as "rule"
    When token "h" sends "PATCH /api/v1/stacks/infra" with '{"vendorConfig":{"terragrunt":{"useSmartSanitization":true}}}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/vendorConfig/terragrunt/useSmartSanitization" as "rule"
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then the answer's vendorConfig's useSmartSanitization is 'false'
    And the answer's vendorConfig's skipReplan is 'false'

  @op:stackCreate @op:stackUpdate @op:stackPatch @case:denial
  Scenario: With vendor-terragrunt switched off, every request that sets the Terragrunt member answers 403 naming it
    Given the installation switches off the features "vendor-terragrunt"
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy","vendorConfig":{"terragrunt":{"useRunAll":true,"useSmartSanitization":false}}},"manageState":true}' through A
    Then it answers a 403 problem "feature-disabled"
    And the problem names the feature "vendor-terragrunt"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    Then it answers 201
    When token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"main","space":"legacy","vendorConfig":{"terragrunt":{"useRunAll":true,"useSmartSanitization":false}}}' through A
    Then it answers a 403 problem "feature-disabled"
    And the problem names the feature "vendor-terragrunt"
    When token "h" sends "PATCH /api/v1/stacks/infra" with '{"vendorConfig":{"terraform":null,"terragrunt":{"useRunAll":true,"useSmartSanitization":false}}}' through B
    Then it answers a 403 problem "feature-disabled"
    And the problem names the feature "vendor-terragrunt"
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then the answer's vendorConfig's kind is '"StackConfigVendorTerraform"'

  @op:stack @op:stacks @op:stackUpdate @op:stackPatch @case:denial
  Scenario: A space reader reads a Terragrunt stack and changes none of it
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And the member key is space-reader on the space "legacy"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy","vendorConfig":{"terragrunt":{"tool":"OPEN_TOFU","terragruntVersion":"~> 0.73.0","useRunAll":true,"useSmartSanitization":false}}},"manageState":true}' through A
    When token "m" eventually reads the stack "infra" through B with the effective versions '{"tool":"OPEN_TOFU","effectiveTerragruntVersion":"0.73.16","effectiveTerraformVersion":"{opentofu.newest}"}'
    Then the answer's canWrite is 'false'
    When token "m" sends "GET /api/v1/stacks" through A
    Then the answer lists the stacks "infra"
    When token "m" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"main","space":"legacy","vendorConfig":{"terragrunt":{"useRunAll":true,"useSmartSanitization":false,"useStateManagement":false}}}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_UPDATE"'
    When token "m" sends "PATCH /api/v1/stacks/infra" with '{"vendorConfig":{"terragrunt":{"terragruntVersion":"0.73.11"}}}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_UPDATE"'
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then the answer's vendorConfig's terragruntVersion is '"~> 0.73.0"'

  @vcs @case:success
  Scenario: A queued run's specification mounts Terragrunt and the OpenTofu it drives, with no state backend when useStateManagement is false
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | terragrunt.hcl | inputs = {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And the launcher "l" registers with the key of "csr" and the certificate "cert" through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terragrunt":{"tool":"OPEN_TOFU","terragruntVersion":"~> 0.73.0","terraformVersion":"~> 1.6.0","useRunAll":false,"useSmartSanitization":false,"useStateManagement":false}}},"manageState":true}' through B
    And token "h" eventually reads the stack "infra" through A with the effective versions '{"tool":"OPEN_TOFU","effectiveTerragruntVersion":"0.73.16","effectiveTerraformVersion":"1.6.3"}'
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through A
    And token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then the answer assigns the run "run", its run token kept as "rt"
    When token "rt" sends "GET /worker/v1/runs/{run}/spec" through A
    Then it answers 200
    And the specification mounts terragrunt "0.73.16" driving tofu "1.6.3", downloaded, without a state backend, in the project root's unit alone

  @vcs @case:success
  Scenario: A tracked run of a Terragrunt stack mounts the newest releases with its state backend, and its apply finishes with no outputs
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | terragrunt.hcl | inputs = {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And the launcher "l" registers with the key of "csr" and the certificate "cert" through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"skipVendorBinaryDownload":true,"vendorConfig":{"terragrunt":{"useRunAll":true,"useSmartSanitization":false}}},"manageState":true}' through B
    And token "h" eventually reads the stack "infra" through A with the effective versions '{"tool":"TERRAFORM_FOSS","effectiveTerragruntVersion":"{terragrunt.newest}","effectiveTerraformVersion":"{terraform.newest}"}'
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through A
    And token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then the answer assigns the run "run", its run token kept as "rt"
    When token "rt" sends "GET /worker/v1/runs/{run}/spec" through A
    Then it answers 200
    And the specification mounts terragrunt "{terragrunt.newest}" driving terraform "{terraform.newest}", not downloaded, with a state backend, across every unit
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"started","state":"INITIALIZING","stateVersion":3}' through B
    Then it answers 200
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"INITIALIZING","stateVersion":4}' through A
    Then it answers 200
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":1,"change":0,"delete":0,"import":0,"outputs":false}}' through B
    Then it answers 200
    And the answer's state is '"APPLYING"'
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"APPLYING","stateVersion":6}' through A
    Then it answers 200
    And the answer's action is '"exit"'
    And the answer's state is '"FINISHED"'
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, APPLYING, FINISHED"
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then the answer's outputs is '[]'

  @vcs @op:stack @case:failure
  Scenario: A Terragrunt constraint no listed release meets reads back null and refuses the specification, naming it
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | terragrunt.hcl | inputs = {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And the launcher "l" registers with the key of "csr" and the certificate "cert" through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Probe","repository":"infra","branch":"main","space":"legacy","vendorConfig":{"terragrunt":{"useRunAll":true,"useSmartSanitization":false}}},"manageState":true}' through B
    And token "h" eventually reads the stack "probe" through A with the effective versions '{"tool":"TERRAFORM_FOSS","effectiveTerragruntVersion":"{terragrunt.newest}","effectiveTerraformVersion":"{terraform.newest}"}'
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terragrunt":{"tool":"OPEN_TOFU","terragruntVersion":"~> 0.50.0","terraformVersion":"~> 1.6.0","useRunAll":true,"useSmartSanitization":false}}},"manageState":true}' through B
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then it answers 200
    And the answer's vendorConfig's effectiveVersions is '{"tool":"OPEN_TOFU","effectiveTerraformVersion":"1.6.3"}'
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through A
    And token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then the answer assigns the run "run", its run token kept as "rt"
    When token "rt" sends "GET /worker/v1/runs/{run}/spec" through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/vendorConfig/terragrunt/terragruntVersion" as "rule"
