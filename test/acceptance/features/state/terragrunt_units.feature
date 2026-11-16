@postgres @objectstore @vcs @docker @terraform @terragrunt
Feature: Runs of a Terragrunt stack, each unit in its own state workspace
  A real launcher of a private pool runs each run of a Terragrunt stack
  in a container of infraweave-runner-terraform:acceptance, with the
  newest Terragrunt and the newest Terraform the version catalogue lists,
  each of which it downloads from the suite's tool mirror and verifies
  against its vendor's signing key, Gruntwork's for Terragrunt. The
  stack's project root is live, whose units are live itself and
  live/network/vpc, which sources its module from modules/vpc; both
  include the repository's root.hcl, which they find in a parent folder.
  Initialization writes each unit's backend file, naming its own state
  workspace, the default one for the unit at the project root and
  network%2Fvpc for the other, and runs init across the units through
  terragrunt run --all; planning plans them all, and the run's delta is
  the sum of their plans'. A tracked run waits for confirmation with its
  workspace saved, Terragrunt's cache of the units with it, and once
  confirmed applies every unit in a new container, so the stack's state
  workspaces are one per unit, each at a version the run wrote. A unit
  that includes a file declaring remote_state fails initialization naming
  the unit; a launcher whose download of Terragrunt is altered fails the
  run in PREPARING naming the release; and a stack deleted with its
  resources destroys every unit, its after-destroy hook finding no
  resource left in any unit's state. The release indexes serve the recorded
  fixtures; the home key is space-admin on the account's top space.
  Stacks live in legacy, keep their state in infraweave and run on the
  pool; the units have terraform_data resources alone, so no provider is
  downloaded.
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
