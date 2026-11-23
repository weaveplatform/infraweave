@kubernetes-lab @kubernetes @postgres @vcs @tofu
Feature: Initialization commands in Kubernetes helper Pods
  A real worker controller evaluates its local initialization policy for
  each run its slot claims, and each exec call of that policy runs in a
  helper Pod of its own: owned by the slot's Worker, of the run's resolved
  runner image, its bootstrap init container receiving only the infraweave
  executable and an empty workspace, and its helper container mounting
  that executable read-only and nothing else, as UID 1983 without
  privilege, capabilities, a service account token or a host path. The
  call's arguments reach the helper only through its attached standard
  input, so they appear in no Pod; the run's secrets reach no helper at
  all. A helper Pod is gone once its call ends, and a call that outlasts
  the policy's deadline fails the run in PREPARING and leaves none.
  ref: decision 0009
  ref: decision 0016

  Background:
    Given the owned Terraform Kubernetes API and namespace are ready
    And the canonical worker CRDs are established in the owned cluster
    And a controller namespace "home" holds the home management key
    And run Pods reach the scenario's servers, and the objects they sign, through the HTTPS front
    And the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = "one"\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And the worker controllers started from now on evaluate the initialization policy within 45s:
      """
      package infraweave
      import future.keywords
      verified := exec(["sh", "-c", "printf %s \"$1\" | sha256sum", "verifier", input.run.token]) if {
        input.static_run_environment.stack_slug == "infra"
      }
      probe := exec(["sh", "-c", "id -u; env; ls -A /mnt/workspace /opt/infraweave/bin"]) if {
        input.static_run_environment.stack_slug == "infra"
      }
      deny contains "Digest mismatch" if {
        input.static_run_environment.stack_slug == "infra"
        not startswith(verified.stdout, crypto.sha256(input.run.token))
      }
      deny contains "Helper identity" if {
        input.static_run_environment.stack_slug == "infra"
        not startswith(probe.stdout, "1983\n")
      }
      deny contains "Helper saw a secret" if { contains(probe.stdout, "s3cr3t") }
      deny contains "Helper saw its request" if { contains(probe.stdout, input.run.token) }
      deny contains "Slow verifier ran" if {
        input.static_run_environment.stack_slug == "slow"
        exec(["sleep", "55"]).status == 0
      }
      """
    And 1 worker controller watch the controller namespaces
    And the Managed WorkerPool "build" of capacity 1 in the space "team" is applied in "home"
    And the WorkerPool "build" eventually has 1 registered slots, its pool kept as "build"
    And the WorkerPool "build" eventually reports ExecutorAvailable True "LauncherRunning"
    And token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first

  @op:runTrigger @case:success
  Scenario: A run's initialization commands verify it in hardened helper Pods of its runner image
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}","workerPool":"{build}","autodeploy":true,"vendorConfig":{"opentofu":{}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_secret","type":"ENVIRONMENT_VARIABLE","value":"s3cr3t-h3lp3r","writeOnly":true}' through A
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, APPLYING, FINISHED"
    And the run "run" ran 2 initialization commands in helper Pods of the WorkerPool "build" from the image "{runner.opentofu}", each hardened and holding no "s3cr3t-h3lp3r"
    And the run "run" eventually has no initialization command Pod
    And the run "run" eventually has no Pod
    And no run Pod, Worker resource or worker controller log line holds "s3cr3t-h3lp3r"

  @op:runTrigger @case:failure
  Scenario: An initialization command that outlasts the policy's deadline fails the run in PREPARING and leaves no helper Pod
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Slow","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}","workerPool":"{build}","vendorConfig":{"opentofu":{}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/slow/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    Then the run "run" eventually runs an initialization command Pod
    And token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note is "worker initialization policy evaluation failed: the initialization command did not finish within its deadline"
    And the run "run" eventually has no initialization command Pod
    And the run "run" eventually has no Pod
