@postgres @objectstore @vcs @docker @terraform @terragrunt
Feature: Terragrunt runs select the root unit or all units and their state workspaces
  A real launcher of a private pool runs each run of a Terragrunt stack
  in a container of infraweave-runner-terraform:acceptance, with the
  newest Terragrunt and the newest Terraform the version catalogue lists,
  each of which it downloads from the suite's tool mirror and verifies
  against its vendor's signing key, Gruntwork's for Terragrunt. The
  stack's project root is live, whose units are live itself and
  live/network/vpc, which sources its module from modules/vpc; both
  include the repository's root.hcl, which they find in a parent folder.
  With run-all, initialization writes each unit's backend file, naming its own state
  workspace, the default one for the unit at the project root and
  network%2Fvpc for the other, and runs init across the units through
  terragrunt run --all; planning plans them all, and the run's delta is
  the sum of their plans'. A tracked run waits for confirmation with its
  workspace saved, Terragrunt's cache of the units with it, and once
  confirmed applies every unit in a new container, so the stack's state
  workspaces are one per unit, each at a version the run wrote. The
  stack's Tofu workspaces list the same workspaces, and each unit's apply
  holds its own workspace's lock: an operator's release of
  network/vpc's lock, which the backend file names network%2Fvpc,
  refuses that unit's write with 423 and the run fails, while the other
  unit's write lands. The relay the launcher reaches the servers through
  holds the backend's writes for that. Whether infraweave keeps a stack's
  state was decided when the stack was created: terragrunt
  use_state_management in a run's runtime configuration changes it
  neither way. A unit that includes a file declaring remote_state fails
  initialization naming the unit; a launcher whose download of Terragrunt
  is altered fails the run in PREPARING naming the release; and a stack
  deleted with its resources destroys every unit, its after-destroy hook
  finding no resource left in any unit's state. The release indexes serve
  the recorded fixtures; the home key is space-admin on the account's top
  space.
  Stacks live in legacy, keep their state in infraweave and run on the
  pool; the units have terraform_data resources alone, so no provider is
  downloaded. Without run-all, only live's root unit runs and writes its
  backend in the default workspace; nested units and explicit stacks are
  untouched. A runtime terragrunt block replaces the stack's run-all mode,
  false when its use_run_all key is absent. A missing root unit fails
  initialization, even where nested units exist.
  ref: decision 0008
  ref: decision 0010
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | root.hcl                        | locals {\n  owner = "infraweave"\n}\n |
      | live/terragrunt.hcl             | include "root" {\n  path = find_in_parent_folders("root.hcl")\n}\n |
      | live/main.tf                    | resource "terraform_data" "root" {\n  input = "root"\n}\n |
      | live/network/vpc/terragrunt.hcl | include "root" {\n  path = find_in_parent_folders("root.hcl")\n}\n\nterraform {\n  source = "../../../modules/vpc"\n}\n |
      | modules/vpc/main.tf             | resource "terraform_data" "vpc" {\n  input = "vpc"\n}\n\nresource "terraform_data" "subnet" {\n  input = terraform_data.vpc.output\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  @op:runTrigger @op:runConfirm @case:success
  Scenario: A tracked run plans every unit, waits for confirmation, and applies each into its own state workspace
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","projectRoot":"live","space":"legacy","workerPool":"{pool}","vendorConfig":{"terragrunt":{"useRunAll":true,"useSmartSanitization":false}}},"manageState":true}' through B
    And token "h" eventually reads the stack "infra" through A with the effective versions '{"tool":"TERRAFORM_FOSS","effectiveTerragruntVersion":"{terragrunt.newest}","effectiveTerraformVersion":"{terraform.newest}"}'
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "UNCONFIRMED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED"
    And token "h" reads the run "run"'s PLANNING log through A, which holds "infraweave: saved the workspace for the plan's confirmation" and not "Error"
    When token "h" sends "GET /api/v1/runs/{run}" through B
    Then the answer's delta is '{"addCount":3,"changeCount":0,"deleteCount":0,"importCount":0,"moveCount":0,"forgetCount":0,"resources":0,"okCount":0,"skipCount":0,"rescueCount":0,"ignoreCount":0,"unreachableCount":0,"failedCount":0}'
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through A
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, CONFIRMED, PREPARING_APPLY, APPLYING, FINISHED"
    And token "h" reads the run "run"'s APPLYING log through A, which holds "Apply complete! Resources: 1 added, 0 changed, 0 destroyed." and not "Error"
    And token "h" reads the run "run"'s APPLYING log through B, which holds "Apply complete! Resources: 2 added, 0 changed, 0 destroyed." and not "Error"
    And the run "run" wrote 2 state versions
    And the state workspaces of the run "run"'s stack are "default, network/vpc", each at a version the run wrote
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then the answer's outputs is '[]'

  @op:searchTofuWorkspaces @op:tofuWorkspaceUnlock @case:success
  Scenario: Each unit's apply holds its own workspace's lock, and the release of network/vpc's refuses that unit's write, failing the run
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","projectRoot":"live","space":"legacy","workerPool":"{pool}","vendorConfig":{"terragrunt":{"useRunAll":true,"useSmartSanitization":false}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "UNCONFIRMED" through B
    When token "h" sends "POST /api/v1/tofu-workspaces/search" with '{"predicates":[{"field":"stack","constraint":{"stringEquals":["infra"]}}]}' through A
    Then the answer's Tofu workspaces are "default, network/vpc"
    And the answer's Tofu workspace "network/vpc" names "vpc"
    When the relay holds the state backend's writes
    And token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through B
    Then the relay holds 1 write of the state of the run "run"
    And the relay holds 1 write of the state workspace "network/vpc" of the run "run"
    When token "h" sends "POST /api/v1/tofu-workspaces/search" with '{"predicates":[{"field":"isLocked","constraint":{"booleanEquals":[true]}}]}' through A
    Then the answer's Tofu workspaces are "default locked by the home key, network/vpc locked by the home key"
    When token "h" sends "POST /api/v1/tofu-workspaces/{vpc}/unlock" through B
    Then the answer's Tofu workspaces are "network/vpc"
    And the audit trail holds 1 release of the Tofu workspace "vpc" by the home key
    When token "h" sends "POST /api/v1/tofu-workspaces/search" with '{"predicates":[{"field":"isLocked","constraint":{"booleanEquals":[true]}}]}' through A
    Then the answer's Tofu workspaces are "default locked by the home key"
    When the relay releases the state backend's writes
    Then token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, CONFIRMED, PREPARING_APPLY, APPLYING, FAILED"
    And the relay passed a POST of the state of the run "run" answered 200
    And the relay passed a POST of the state workspace "network/vpc" of the run "run" answered 423
    And the relay passed an UNLOCK of the state workspace "network/vpc" of the run "run" answered 423
    And the run "run" wrote 1 state version

  @op:runTrigger @case:success
  Scenario: A run's runtime configuration changes no stack's state management: use_state_management off keeps each unit's state, and on keeps none
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Kept","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","projectRoot":"live","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"terragrunt":{"useRunAll":true,"useSmartSanitization":false}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Unkept","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","projectRoot":"live","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"terragrunt":{"useRunAll":true,"useSmartSanitization":false,"useStateManagement":false}}},"manageState":true}' through A
    When token "h" sends "POST /api/v1/stacks/kept/runs" with '{"commitSha":"{seed}","runtimeConfig":{"yaml":"terragrunt:\n  use_state_management: false\n  use_run_all: true\n"}}' through A
    And the answer's id names "kept"
    Then token "h" eventually reads the run "kept" in the state "FINISHED" through B
    And the run "kept" wrote 2 state versions
    And the state workspaces of the run "kept"'s stack are "default, network/vpc", each at a version the run wrote
    When token "h" sends "GET /api/v1/stacks/kept" through A
    Then the answer's managesStateFile is 'true'
    When token "h" sends "POST /api/v1/stacks/unkept/runs" with '{"commitSha":"{seed}","runtimeConfig":{"yaml":"terragrunt:\n  use_state_management: true\n  use_run_all: true\n"}}' through B
    And the answer's id names "unkept"
    Then token "h" eventually reads the run "unkept" in the state "FINISHED" through A
    And token "h" reads the run "unkept"'s APPLYING log through B, which holds "Apply complete! Resources: 2 added, 0 changed, 0 destroyed." and not "Error"
    And the run "unkept" wrote 0 state versions
    When token "h" sends "POST /api/v1/tofu-workspaces/search" with '{"predicates":[{"field":"stack","constraint":{"stringEquals":["unkept"]}}]}' through A
    Then the answer's Tofu workspaces are ""
    When token "h" sends "GET /api/v1/stacks/unkept" through B
    Then the answer's managesStateFile is 'false'

  @op:runTrigger @case:failure
  Scenario: A unit that includes a file declaring remote_state fails initialization naming the unit
    Given a Gitea repository "own" with the files:
      | root.hcl                | remote_state {\n  backend = "local"\n  config  = {}\n}\n |
      | live/app/terragrunt.hcl | include "root" {\n  path = find_in_parent_folders("root.hcl")\n}\n |
      | live/app/main.tf        | resource "terraform_data" "app" {}\n |
    And the head of "main" of "own" is kept as "own_seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Own","repository":"own","branch":"main","provider":"GIT","repositoryURL":"{own.url}","projectRoot":"live","space":"legacy","workerPool":"{pool}","vendorConfig":{"terragrunt":{"useRunAll":true,"useSmartSanitization":false}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/own/runs" with '{"commitSha":"{own_seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, FAILED"
    And the answer's last state transition's note is 'The state backend cannot be configured: the unit "app" declares its own state backend: remote_state in ../root.hcl, which it includes.'
    And the run "run" wrote 0 state versions

  @op:runTrigger @case:failure
  Scenario: A launcher whose download of Terragrunt is altered fails the run in PREPARING, naming the release
    Given the launchers started from now on download terragrunt's build altered
    And a certificate signing request for a P-256 key is kept as "csr_altered"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Altered","space":"legacy","certificateSigningRequest":"{csr_altered}"}' through A
    And the answer's id names "altered"
    And token "h" starts the launcher "a" of the pool "altered" with the key of "csr_altered" through B
    And token "h" eventually reads the worker "a" of the pool "altered" as "IDLE" through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","projectRoot":"live","space":"legacy","workerPool":"{altered}","vendorConfig":{"terragrunt":{"useRunAll":true,"useSmartSanitization":false}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note starts with 'terragrunt {terragrunt.newest} for linux/{launcher.arch}: download terragrunt_linux_{launcher.arch}: the SHA-256 is not the signed one: '

  @op:stackDelete @case:success
  Scenario: A stack deleted with its resources destroys every unit in a destroy run, and is deleted when the run finishes
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","projectRoot":"live","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"terragrunt":{"useRunAll":true,"useSmartSanitization":false}},"afterDestroy":["left=$(terragrunt run --all --summary-disable -- state list 2>/dev/null) && test -z \"$left\""]},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the state workspaces of the run "run"'s stack are "default, network/vpc", each at a version the run wrote
    When token "h" sends "DELETE /api/v1/stacks/infra?destroyResources=true" through A
    Then it answers 200
    And the answer's deleting is 'true'
    When token "h" sends "GET /api/v1/stacks/infra/runs" through B
    Then the answer's first run names "destroy"
    And token "h" eventually finds no stack "infra" through A
    And the outbox holds the run "destroy"'s run_updated events of the states "QUEUED, READY, PREPARING, INITIALIZING, DESTROYING, FINISHED"

  @op:runTrigger @op:runConfirm @op:stackDelete @case:success
  Scenario: A single-unit run plans and applies only the project root, then destroys it
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","projectRoot":"live","space":"legacy","workerPool":"{pool}","vendorConfig":{"terragrunt":{"useRunAll":false,"useSmartSanitization":false}},"beforeInit":["set -x"]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "UNCONFIRMED" through B
    And the answer's delta is '{"addCount":1,"changeCount":0,"deleteCount":0,"importCount":0,"moveCount":0,"forgetCount":0,"resources":0,"okCount":0,"skipCount":0,"rescueCount":0,"ignoreCount":0,"unreachableCount":0,"failedCount":0}'
    And token "h" reads the run "run"'s INITIALIZING log through A, which holds "terragrunt run -- init" and not "--all"
    And token "h" reads the run "run"'s PLANNING log through B, which holds "terragrunt run -- plan" and not "--all"
    And token "h" reads the run "run"'s PLANNING log through A, which holds "terragrunt run -- show -json tfplan.tfplan" and not "--all"
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through A
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And token "h" reads the run "run"'s APPLYING log through A, which holds "Resources: 1 added" and not "--all"
    And the run "run" wrote 1 state version
    And the state workspaces of the run "run"'s stack are "default", each at a version the run wrote
    When token "h" sends "DELETE /api/v1/stacks/infra?destroyResources=true" through A
    Then it answers 200
    When token "h" sends "GET /api/v1/stacks/infra/runs" through B
    Then the answer's first run names "destroy"
    And token "h" eventually finds no stack "infra" through A
    And the outbox holds the run "destroy"'s run_updated events of the states "QUEUED, READY, PREPARING, INITIALIZING, DESTROYING, FINISHED"

  @op:runTrigger @case:success
  Scenario Outline: The runtime block replaces the stack's run-all mode, and an absent block keeps it
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","projectRoot":"live","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"terragrunt":{"useRunAll":<stack>,"useSmartSanitization":false}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runtimeConfig":{"yaml":"<yaml>"}}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the run "run" wrote <versions> state version
    And the state workspaces of the run "run"'s stack are "<workspaces>", each at a version the run wrote

    Examples:
      | stack | yaml                              | versions | workspaces           |
      | true  | terragrunt: {use_run_all: false}   | 1        | default              |
      | false | terragrunt: {use_run_all: true}    | 2        | default, network/vpc |
      | true  | terragrunt: {}                     | 1        | default              |
      | true  | version: 1                        | 2        | default, network/vpc |

  @op:runTrigger @case:failure
  Scenario Outline: A malformed runtime block is refused before a run exists
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","projectRoot":"live","space":"legacy","workerPool":"{pool}","vendorConfig":{"terragrunt":{"useRunAll":true,"useSmartSanitization":false}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runtimeConfig":{"yaml":"<yaml>"}}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/runtimeConfig/yaml" as "format"
    When token "h" sends "GET /api/v1/stacks/infra/runs" through B
    Then the answer's items is '[]'

    Examples:
      | yaml                               |
      | terragrunt: {use_run_all: \"yes\"} |
      | terragrunt: [                      |
      | terragrunt: {}\\n---\\n[           |
      | terragrunt: {}\\n---\\nversion: 1  |

  @op:runTrigger @case:failure
  Scenario: A single-unit run needs terragrunt.hcl at the project root even when nested units exist
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","projectRoot":"live/network","space":"legacy","workerPool":"{pool}","vendorConfig":{"terragrunt":{"useRunAll":false,"useSmartSanitization":false}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, FAILED"
    And the answer's last state transition's note is "The project root live/network holds no terragrunt.hcl; a Terragrunt stack whose useRunAll is false runs the unit there."
    And the run "run" wrote 0 state versions

  @op:runTrigger @case:success
  Scenario: A single-unit run leaves nested own backends and explicit stacks untouched
    Given a Gitea repository "single" with the files:
      | live/terragrunt.hcl                 | locals {\n  owner = "infraweave"\n}\n |
      | live/main.tf                        | resource "terraform_data" "root" {\n  input = "root"\n}\n |
      | live/nested/terragrunt.hcl          | remote_state {\n  backend = "local"\n  config = {}\n}\n |
      | live/nested/main.tf                 | resource "terraform_data" "ignored" {}\n |
      | live/nested/terragrunt.stack.hcl    | unit "ignored" {\n  source = "unused"\n  path = "generated"\n}\n |
    And the head of "main" of "single" is kept as "single_seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Single","repository":"single","branch":"main","provider":"GIT","repositoryURL":"{single.url}","projectRoot":"live","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"terragrunt":{"useRunAll":false,"useSmartSanitization":false}},"afterInit":["test ! -e nested/infraweave_backend.tf && echo nested-backend-untouched"]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/single/runs" with '{"commitSha":"{single_seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FINISHED" through B
    And token "h" reads the run "run"'s INITIALIZING log through A, which holds "nested-backend-untouched" and not "Error"
    And the run "run" wrote 1 state version
    And the state workspaces of the run "run"'s stack are "default", each at a version the run wrote

  @op:runTrigger @case:failure
  Scenario: Every vendor refuses malformed custom runtime YAML before a run exists
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","projectRoot":"live","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runtimeConfig":{"yaml":"terragrunt: ["}}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/runtimeConfig/yaml" as "format"
    When token "h" sends "GET /api/v1/stacks/infra/runs" through B
    Then the answer's items are the runs ""
