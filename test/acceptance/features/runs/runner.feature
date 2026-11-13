@postgres @objectstore @vcs @docker
Feature: The runner executes a run's phases and their hooks in one shell session
  The runner, the run container's root process, reads the run its
  launcher hands it on standard input and executes it: it reports the
  first phase started, then runs each phase the server's answers name,
  INITIALIZING, PLANNING and APPLYING, each between its before- and
  after-hooks, in one sh session, so a variable a hook exports is set in
  every later hook. The first hook or command that fails fails its phase,
  and the afterRun hooks run once the run ends, with its final state.
  Each phase's output is masked, written to standard output and uploaded
  as the state's log before the phase's report; a word a hook names with
  ::add-mask is masked from then on, and the afterRun hooks' output goes
  to standard output alone.
  Here the scenario is the launcher: it registers with the pool's key,
  claims a run, opens its sealed specification, checks out its commit
  from Gitea and starts this build's runner in a container of the tool's
  runner image, infraweave-runner-terraform:acceptance or
  infraweave-runner-opentofu:acceptance, with the pinned terraform or
  tofu at /bin. The container reaches the server through the scenario's
  relay. Stack infra keeps its state in infraweave, runs on the pool and
  has hooks before initialization, after planning and after the run; its
  configuration has a terraform_data resource, so no provider is
  downloaded. The release indexes serve the recorded fixtures, so the
  run's specification names a release. The home key is space-admin on
  the account's top space.
  ref: decision 0008
  ref: decision 0010
  ref: decision 0013
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = "one"\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And the launcher "w" registers with the key of "csr" and the certificate "cert" through A

  @case:success
  Scenario Outline: A tracked run on an autodeploy stack initializes, plans and applies in one container
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":<vendor>,"beforeInit":["export GREETING=hello"],"afterPlan":["echo \"after plan: $GREETING\"","echo ::add-mask sw0rdfish","echo fish sw0rdfish"],"afterRun":["echo \"after run: $TF_VAR_infraweave_final_run_state $GREETING\""]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through A
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "run", its run token kept as "rt"
    And the worker takes over the run "run" with token "rt" and the key of "csr", checking out "infra", through A
    Then the runner executes the run "run" with <tool> and exits 0
    And the runner's output of the run "run" holds "infraweave: working on commit"
    And the runner's output of the run "run" holds "after plan: hello"
    And the runner's output of the run "run" holds "Apply complete! Resources: 1 added, 0 changed, 0 destroyed."
    And the runner's output of the run "run" holds "after run: FINISHED hello"
    And the runner's output of the run "run" holds "fish *****"
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, APPLYING, FINISHED"
    And the run "run" wrote 1 state version
    And the relay passed a LOCK of the state of the run "run" answered 200
    And token "h" reads the run "run"'s INITIALIZING log through A, which holds "infraweave: working on commit" and not "after plan"
    And token "h" reads the run "run"'s PLANNING log through B, which holds "after plan: hello\n::add-mask *****\nfish *****" and not "sw0rdfish"
    And token "h" reads the run "run"'s APPLYING log through A, which holds "Apply complete! Resources: 1 added" and not "after run"

    @terraform
    Examples: Terraform
      | tool      | vendor           |
      | terraform | {"terraform":{}} |

    @tofu
    Examples: OpenTofu
      | tool | vendor          |
      | tofu | {"opentofu":{}} |

  @case:success @terraform
  Scenario: A tracked run on a stack without autodeploy pauses after planning, and its runner exits 0
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","afterRun":["echo after run"]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through A
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "run", its run token kept as "rt"
    And the worker takes over the run "run" with token "rt" and the key of "csr", checking out "infra", through A
    Then the runner executes the run "run" with terraform and exits 0
    And the runner's output of the run "run" holds "Plan: 1 to add, 0 to change, 0 to destroy."
    And token "h" eventually reads the run "run" in the state "UNCONFIRMED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED"
    And the run "run" wrote 0 state versions

  @case:failure @terraform
  Scenario: A failing before-hook fails its phase without running it, and afterRun runs with the final state
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"beforePlan":["echo before plan","exit_code=7; (exit 7)"],"afterPlan":["echo after plan"],"afterRun":["echo \"after run: $TF_VAR_infraweave_final_run_state\""]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through A
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "run", its run token kept as "rt"
    And the worker takes over the run "run" with token "rt" and the key of "csr", checking out "infra", through A
    Then the runner executes the run "run" with terraform and exits 0
    And the runner's output of the run "run" holds "before plan"
    And the runner's output of the run "run" holds "after run: FAILED"
    And token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FAILED"
    And the answer's last state transition's note is "The before_plan hook 2 of 2 failed with exit status 7."
    And the run "run" wrote 0 state versions
