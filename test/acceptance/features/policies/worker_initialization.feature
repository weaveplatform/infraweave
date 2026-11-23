@postgres @objectstore @vcs @docker @terraform @worker-initialization
Feature: Worker local initialization admits a run before source and tool startup
  These programs are infraweave's authored input contract, not a C68 vendor capture.
  Server INITIALIZATION remains a separate READY admission gate. A policy's exec
  calls run, each in a fresh Docker container of the run's resolved runner image
  whose arguments arrive only on its standard input, never on the launcher's
  host; every helper is removed once its call ends, and a stopped or killed
  launcher leaves none behind.
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

  @op:taskCreate @case:success
  Scenario: Initialization commands verify a task in isolated helpers of its runner image
    Given token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_secret","type":"ENVIRONMENT_VARIABLE","value":"s3cr3t-h3lp3r","writeOnly":true}' through A
    And the launchers started from now on evaluate a REGO_V1 initialization policy:
      """
      package infraweave
      import future.keywords
      digest := exec(["sh", "-c", "printf %s \"$1\" | sha256sum", "verifier", input.run.token])
      probe := exec(["sh", "-c", "id -u; id -g; env; ls -A /mnt/workspace /opt/infraweave/bin; tr '\\0' ' ' < /proc/1/cmdline"])
      refused := exec(["sh", "-c", "echo refused >&2; exit 3"])
      missing := exec(["infraweave-no-such-verifier"])
      deny contains "Verifier failed" if { digest.status != 0 }
      deny contains "Digest mismatch" if { not startswith(digest.stdout, crypto.sha256(input.run.token)) }
      deny contains "Helper identity" if { not startswith(probe.stdout, "1983\n1983\n") }
      deny contains "Helper saw a secret" if { contains(probe.stdout, "s3cr3t") }
      deny contains "Helper saw a workspace" if { contains(probe.stdout, "main.tf") }
      deny contains "Helper saw its request" if { contains(probe.stdout, input.run.token) }
      deny contains "Helper entrypoint" if { not contains(probe.stdout, "/opt/infraweave/bin/infraweave runner command") }
      deny contains "Nonzero exit lost" if { refused.status != 3 }
      deny contains "Nonzero exit lost" if { refused.stderr != "refused\n" }
      deny contains "Missing verifier ran" if { missing.status != 127 }
      flag contains "observed" if { print("worker-policy-checked"); print(input.run.token); print(probe.stdout) }
      """
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"terraform version","skipInitialization":true}' through A
    Then it answers 201
    And the answer's id names "run"
    When token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    Then token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PERFORMING, FINISHED"
    And the launcher "l" masked its initialization token in print and reports
    And the run "run" eventually has no initialization command container

  @op:runTrigger @case:failure
  Scenario Outline: An initialization command that <outcome> fails the real handover closed and leaves no helper
    Given the launchers started from now on evaluate a REGO_V1 initialization policy within <budget>:
      """
      package infraweave
      import future.keywords
      results := [exec(argv) | some argv in <calls>]
      deny contains "Every command ran" if { count(results) > 0 }
      """
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "run"
    When token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    Then token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note is "worker initialization policy evaluation failed: <reason>"
    And the run "run" never started a container
    And the run "run" eventually has no initialization command container

    Examples:
      | outcome                 | budget | calls                                                                         | reason                                                         |
      | outlasts its deadline   | 5s     | [["sleep", "30"]]                                                             | the initialization command did not finish within its deadline |
      | writes beyond its bound | 60s    | [["head", "-c", "1048577", "/dev/zero"]]                                      | the initialization command's output exceeded its bounds       |
      | is a ninth call         | 60s    | [["true"], ["true"], ["true"], ["true"], ["true"], ["true"], ["true"], ["true"], ["true"]] | the initialization policy ran more than 8 commands |

  @op:runTrigger @case:failure
  Scenario: An initialization command whose runner image cannot be pulled fails the real handover closed
    Given the launchers started from now on evaluate a REGO_V1 initialization policy:
      """
      package infraweave
      deny contains "Verifier ran" if { exec(["true"]).status == 0 }
      """
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "run"
    When token "h" starts the launcher "l" of the pool "pool" with the key of "csr", whose default runner image is "127.0.0.1:1/none:1" through A
    Then token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note is "worker initialization policy evaluation failed: the initialization command's runner image is unavailable"
    And the run "run" never started a container
    And the run "run" eventually has no initialization command container

  @op:runTrigger @case:failure
  Scenario: A launcher stopped during an initialization command removes its helper
    Given the launchers started from now on evaluate a REGO_V1 initialization policy:
      """
      package infraweave
      deny contains "Slow verifier failed" if { exec(["sleep", "55"]).status != 0 }
      """
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "run"
    When token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    Then the run "run" eventually runs an initialization command
    When the launcher "l" is stopped
    Then the run "run" eventually has no initialization command container
    And the run "run" has no run container

  @op:runTrigger @case:failure
  Scenario: A launcher killed during an initialization command removes the helper it left when it restarts
    Given the launchers started from now on evaluate a REGO_V1 initialization policy:
      """
      package infraweave
      deny contains "Slow verifier failed" if { exec(["sleep", "55"]).status != 0 }
      """
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "run"
    When token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    Then the run "run" eventually runs an initialization command
    When the launcher "l" is killed
    Then the run "run" still has an initialization command container
    When the launcher "l" starts again and resumes its worker
    Then the run "run" eventually has no initialization command container
    And token "h" eventually reads the run "run" in the state "FAILED" through B
    And the run "run" has no run container
