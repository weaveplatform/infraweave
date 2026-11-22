@postgres @objectstore @vcs @docker @terraform @worker-initialization
Feature: Worker local initialization admits a run before source and tool startup
  These programs are infraweave's authored input contract, not a C68 vendor capture.
  Server INITIALIZATION remains a separate READY admission gate.
  ref: decisions 0008, 0009 and 0013

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = "worker-policy"\n} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A

  @op:taskCreate @case:success
  Scenario Outline: A local <engine> helper and comprehension admits the actual task handover
    Given the launchers started from now on evaluate a <engine> initialization policy:
      """
      package infraweave
      import future.keywords
      allowed if {
        input.version == 1
        input.run.type == "TASK"
        input.run.user_provided_metadata == []
        input.run.commit.branch == "main"
        input.static_run_environment.stack_slug == "infra"
        input.static_run_environment.stack_id != "infra"
        input.static_run_environment.space == "legacy"
        input.static_run_environment.pool_id != ""
        input.static_run_environment.account_id != ""
        startswith(input.static_run_environment.runner_image, "ghcr.io/")
        input.tool.name == "terraform"
        input.terragrunt == null
      }
      keys := {key | some key in object.keys(input)}
      deny contains "Unexpected worker snapshot" if { not allowed }
      deny contains "Unexpected input fields" if { keys != {"version", "run", "static_run_environment", "tool", "terragrunt"} }
      deny contains "Undefined helper must not deny" if { input.missing }
      flag contains "observed" if { print("worker-policy-checked"); print(input.run.token) }
      """
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"terraform version","skipInitialization":true}' through A
    Then it answers 201
    And the answer's id names "run"
    When token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    Then token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PERFORMING, FINISHED"
    And token "h" reads the run "run"'s PERFORMING log through A, which holds "Terraform v{terraform.newest}" and not "Error"
    And the launcher "l" masked its initialization token in print and reports

    Examples:
      | engine  |
      | REGO_V0 |
      | REGO_V1 |

  @op:runTrigger @case:failure
  Scenario Outline: A local <engine> deny uses the ordinary PREPARING failure report
    Given the launchers started from now on evaluate a <engine> initialization policy:
      """
      package infraweave
      import future.keywords
      blocked := [kind | some kind in [input.run.type]; kind == "TRACKED"]
      deny contains "Tracked work refused locally" if { count(blocked) == 1; print("worker-policy-checked"); print(input.run.token) }
      """
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "run"
    When token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    Then token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note is "worker initialization policy denied: Tracked work refused locally"
    And the run "run" never started a container
    And the launcher "l" masked its initialization token in print and reports

    Examples:
      | engine  |
      | REGO_V0 |
      | REGO_V1 |

  @op:runTrigger @case:failure
  Scenario: A token echoed by a local policy is masked in the ordinary failure and print
    Given the launchers started from now on evaluate a REGO_V1 initialization policy:
      """
      package infraweave
      deny contains input.run.token if { print("worker-policy-checked"); print(input.run.token) }
      """
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "run"
    When token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    Then token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note is "worker initialization policy denied: *****"
    And the run "run" never started a container
    And the launcher "l" masked its initialization token in print and reports

  @op:runTrigger @case:failure
  Scenario: A local V0 policy with an invalid decision fails the real handover safely
    Given the launchers started from now on evaluate a REGO_V0 initialization policy:
      """
      package infraweave
      deny[42] { true }
      """
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "run"
    When token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    Then token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note is "worker initialization policy evaluation failed"
    And the run "run" never started a container
