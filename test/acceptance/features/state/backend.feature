@postgres @objectstore @vcs @docker
Feature: Terraform and OpenTofu keep their state in infraweave
  A run's tool reaches the managed state backend at the path its
  specification names, behind the address its worker reaches the worker
  API at, with HTTP Basic credentials of the user run and its run token,
  which the agent puts in the tool's environment beside the backend file
  it writes. Here the scenario is the worker: it registers with the
  pool's key, claims a run, opens its sealed specification, checks out
  its commit from Gitea, writes the backend file, reports each phase, and
  runs the pinned terraform or tofu in a container of
  infraweave-runner:acceptance. The container reaches the server through
  the scenario's relay, which records each call and each 423's body, and
  can hold the backend's UNLOCK calls so a lock stays held while another
  process asks for one. A run that initializes or plans holds a shared
  lock, a run that applies the exclusive one, and a run that ended holds
  none and is refused. The servers keep their objects in the scenario's
  RustFS bucket, each state version sealed. Stack infra keeps its state
  in infraweave, deploys automatically and runs on the pool; its
  configuration has a terraform_data resource and outputs, one of them
  sensitive, so no provider is downloaded. The home key is space-admin
  on the account's top space.
  ref: decision 0010
  ref: decision 0016

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = "one"\n}\n\noutput "input" {\n  value = terraform_data.this.output\n}\n\noutput "token" {\n  value     = "plain-s3cret-0451"\n  sensitive = true\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true},"manageState":true}' through B
    And the launcher "w" registers with the key of "csr" and the certificate "cert" through A

  @case:success
  Scenario Outline: A tracked run's init, plan and apply keep its state in infraweave, and a second apply waits on the first's lock
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through A
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "run", its run token kept as "rt"
    And the worker takes over the run "run" with token "rt" and the key of "csr", checking out "infra", through A
    And the worker reports the run "run" started in INITIALIZING through B
    Then <tool> "init" of the run "run" exits 0
    When the worker reports the run "run" succeeded in INITIALIZING through A
    Then the report's answer is to execute in PLANNING
    And <tool> "plan" of the run "run" exits 0
    And the relay passed a LOCK of the state of the run "run" answered 200
    And the relay passed an UNLOCK of the state of the run "run" answered 200
    And the run "run" wrote 0 state versions
    When the worker reports the run "run" succeeded in PLANNING with the delta '{"add":1,"change":0,"delete":0,"import":0,"outputs":true}' through B
    Then the report's answer is to execute in APPLYING
    When the relay holds the state backend's unlocks
    And <tool> "apply -auto-approve" of the run "run" starts as "first"
    And the relay holds 1 unlock of the state of the run "run"
    And <tool> "apply -auto-approve -lock-timeout=300s" of the run "run" starts as "second"
    Then the relay passed a LOCK of the state of the run "run" answered 423 with the LockInfo of its exclusive lock
    When the relay releases the state backend's unlocks
    Then the process "first" exits 0
    And the process "second" exits 0
    And the relay passed a POST of the state of the run "run" answered 200
    And the run "run" wrote 1 state version
    And <tool> "state pull" of the run "run" exits 0
    And the current state version is the run "run"'s, with the serial and lineage of its last output
    And <tool> "output -raw token" of the run "run" exits 0
    And the last output of the run "run" is "plain-s3cret-0451"
    And no object in the scenario's bucket holds "plain-s3cret-0451", of at least 1 object
    When the worker reports the run "run" succeeded in APPLYING through A
    Then the report's answer is to exit in FINISHED
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, APPLYING, FINISHED"
    And <tool> "state pull" of the run "run" exits 1
    And the relay passed a GET of the state of the run "run" answered 403

    @terraform
    Examples: Terraform
      | tool      |
      | terraform |

    @tofu
    Examples: OpenTofu
      | tool |
      | tofu |

  @case:success
  Scenario Outline: Two proposed runs' plans hold shared locks on the state at once
    Given a commit to "feature" of "infra" changing "notes.txt"
    And the head of "feature" of "infra" is kept as "change"
    And the launcher "w2" registers with the key of "csr" and the certificate "cert" through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And the answer's id names "one"
    And token "h" eventually reads the run "one" in the state "READY" through B
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through A
    And the answer assigns the run "one", its run token kept as "t1"
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{change}"}' through B
    And the answer's id names "two"
    And token "h" eventually reads the run "two" in the state "READY" through A
    And token "w2" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "two", its run token kept as "t2"
    And the worker takes over the run "one" with token "t1" and the key of "csr", checking out "infra", through A
    And the worker takes over the run "two" with token "t2" and the key of "csr", checking out "infra", through B
    And the worker reports the run "one" started in INITIALIZING through A
    And the worker reports the run "two" started in INITIALIZING through B
    And <tool> "init" of the run "one" exits 0
    And <tool> "init" of the run "two" exits 0
    And the worker reports the run "one" succeeded in INITIALIZING through B
    And the worker reports the run "two" succeeded in INITIALIZING through A
    And the relay holds the state backend's unlocks
    And <tool> "plan" of the run "one" starts as "first"
    And the relay holds 1 unlock of the state of the run "one"
    And <tool> "plan" of the run "two" starts as "second"
    Then the relay holds 2 unlocks of the state of the run "two"
    And the state of the run "one" is held by the shared locks of the runs "one, two"
    When the relay releases the state backend's unlocks
    Then the process "first" exits 0
    And the process "second" exits 0
    When the worker reports the run "one" succeeded in PLANNING with the delta '{"add":1,"change":0,"delete":0,"import":0,"outputs":true}' through A
    Then the report's answer is to exit in FINISHED
    When the worker reports the run "two" succeeded in PLANNING with the delta '{"add":1,"change":0,"delete":0,"import":0,"outputs":true}' through B
    Then the report's answer is to exit in FINISHED
    And the run "one" wrote 0 state versions
    And the run "two" wrote 0 state versions

    @terraform
    Examples: Terraform
      | tool      |
      | terraform |

    @tofu
    Examples: OpenTofu
      | tool |
      | tofu |
