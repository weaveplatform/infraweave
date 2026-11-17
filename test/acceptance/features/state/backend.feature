@postgres @objectstore @vcs @docker
Feature: Terraform and OpenTofu keep their state in infraweave
  A run's tool reaches the managed state backend at the path its
  specification names, behind the address its worker reaches the worker
  API at, with HTTP Basic credentials of the user run and its run token,
  which the agent puts in the tool's environment beside the backend file
  it writes. Here the scenario is the worker: it registers with the
  pool's key, claims a run, opens its sealed specification, checks out
  its commit from Gitea, writes the backend file, reports each phase, and
  runs the pinned terraform or tofu in a container of its runner image,
  infraweave-runner-terraform:acceptance or
  infraweave-runner-opentofu:acceptance, which the suite builds from
  deploy/images. The container reaches the server through
  the scenario's relay, which records each call and each 423's body, and
  can hold the backend's UNLOCK calls so a lock stays held while another
  process asks for one, or its writes so an apply holds its lock with its
  write still to come. A run that initializes or plans holds a shared
  lock, a run that applies the exclusive one, and a run that ended holds
  none and is refused. The backend's locks are the stack's Tofu
  workspaces', its default one there from the stack's creation: an
  operator searches them and releases a stuck one, and the run whose lock
  goes is refused its next write and its own release of the lock. The
  servers keep their objects in the scenario's RustFS bucket, each state
  version sealed, and a download URL the home key signs reads the current
  version back unsealed, with no credential. Stack infra keeps its state
  in infraweave with external state access on, deploys automatically and
  runs on the pool; its configuration has a terraform_data resource and
  outputs, one of them sensitive, so no provider is downloaded. The
  release indexes serve the recorded fixtures, so the run's specification
  names a Terraform release. The home key is space-admin on the account's
  top space.
  OpenTofu's encrypted state stays an envelope with its clear serial and
  lineage; successive encrypted applies advance the serial, and neither
  an import nor a download decrypts it. A run without the key cannot plan
  or replace that state, and neither a proposed run nor an API key may
  post a new version.
  ref: decision 0010
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = fileexists("${path.module}/changed") ? "two" : "one"\n}\n\noutput "input" {\n  value = terraform_data.this.output\n}\n\noutput "token" {\n  value     = "plain-s3cret-0451"\n  sensitive = true\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"terraform":{"externalStateAccessEnabled":true}}},"manageState":true}' through B
    And the launcher "w" registers with the key of "csr" and the certificate "cert" through A

  @op:stateDownloadUrl @case:success
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
    When token "h" sends "POST /api/v1/stacks/infra/state/download-url" with '{}' through A
    Then it answers 200
    And the answer's url is kept as "dl"
    And the URL "dl" read with no credential through B is the current state version of the run "run", holding "plain-s3cret-0451"
    And <tool> "output -raw token" of the run "run" exits 0
    And the last output of the run "run" is "plain-s3cret-0451"
    And no object in the scenario's bucket holds "plain-s3cret-0451", of at least 1 object
    And <tool> "output -json" of the run "run" exits 0
    And the worker records the last output of the run "run" as its outputs.json through B
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

  @case:success @op:searchTofuWorkspaces @op:searchTofuWorkspacesSuggestions @op:tofuWorkspaceUnlock
  Scenario Outline: A new stack lists its default Tofu workspace, an apply's lock is that workspace's, and an operator's release of it refuses the apply's write and release
    When token "h" sends "POST /api/v1/tofu-workspaces/search" with '{"predicates":[{"field":"stack","constraint":{"stringEquals":["infra"]}}]}' through A
    Then the answer's Tofu workspaces are "default"
    And the answer's Tofu workspace "default" names "ws"
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
    When token "h" sends "POST /api/v1/tofu-workspaces/search" with '{"predicates":[{"field":"stack","constraint":{"stringEquals":["infra"]}}]}' through B
    Then the answer's Tofu workspaces are "default"
    When the worker reports the run "run" succeeded in PLANNING with the delta '{"add":1,"change":0,"delete":0,"import":0,"outputs":true}' through A
    Then the report's answer is to execute in APPLYING
    When the relay holds the state backend's writes
    And <tool> "apply -auto-approve" of the run "run" starts as "apply"
    And the relay holds 1 write of the state of the run "run"
    And token "h" sends "POST /api/v1/tofu-workspaces/search" with '{"predicates":[{"field":"isLocked","constraint":{"booleanEquals":[true]}}]}' through A
    Then the answer's Tofu workspaces are "default locked by the home key"
    And the state of the run "run" is held by the exclusive locks of the runs "run"
    When token "h" sends "POST /api/v1/tofu-workspaces/search/suggestions" with '{"fields":["name","stack"]}' through B
    Then the answer suggests the name values "default:1" of 1 Tofu workspace
    And the answer suggests the stack values "infra:1" of 1 Tofu workspace
    When token "h" sends "POST /api/v1/tofu-workspaces/{ws}/unlock" through B
    Then the answer's Tofu workspaces are "default"
    And the audit trail holds 1 release of the Tofu workspace "ws" by the home key
    When token "h" sends "POST /api/v1/tofu-workspaces/{ws}/unlock" through A
    Then the answer's Tofu workspaces are "default"
    And the audit trail holds 1 release of the Tofu workspace "ws" by the home key
    When the relay releases the state backend's writes
    Then the process "apply" exits 1
    And the relay passed a POST of the state of the run "run" answered 423
    And the relay passed an UNLOCK of the state of the run "run" answered 423
    And the run "run" wrote 0 state versions

    @terraform
    Examples: Terraform
      | tool      |
      | terraform |

    @tofu
    Examples: OpenTofu
      | tool |
      | tofu |

  @terraform @case:success @op:tofuWorkspaceUnlockAll
  Scenario: Releasing a stack's Tofu workspaces frees a planning run's shared lock, answers the workspace it held and refuses the plan's own release
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through B
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through A
    And the answer assigns the run "run", its run token kept as "rt"
    And the worker takes over the run "run" with token "rt" and the key of "csr", checking out "infra", through B
    And the worker reports the run "run" started in INITIALIZING through A
    And terraform "init" of the run "run" exits 0
    And the worker reports the run "run" succeeded in INITIALIZING through B
    And the relay holds the state backend's unlocks
    And terraform "plan" of the run "run" starts as "plan"
    And the relay holds 1 unlock of the state of the run "run"
    When token "h" sends "POST /api/v1/stacks/infra/tofu-workspaces/unlock" through A
    Then the answer's Tofu workspaces are "default"
    And the answer's Tofu workspace "default" names "ws"
    And the audit trail holds 1 release of the Tofu workspace "ws" by the home key
    When token "h" sends "POST /api/v1/stacks/infra/tofu-workspaces/unlock" through B
    Then the answer's unlockedWorkspaces is '[]'
    When token "h" sends "POST /api/v1/tofu-workspaces/search" with '{"predicates":[{"field":"isLocked","constraint":{"booleanEquals":[true]}}]}' through A
    Then the answer's Tofu workspaces are ""
    When the relay releases the state backend's unlocks
    Then the process "plan" exits 1
    And the relay passed an UNLOCK of the state of the run "run" answered 423
    And the audit trail holds 1 release of the Tofu workspace "ws" by the home key

  @terraform @case:denial @op:searchTofuWorkspaces @op:searchTofuWorkspacesSuggestions @op:tofuWorkspaceUnlock @op:tofuWorkspaceUnlockAll
  Scenario: A caller who reads no space sees no Tofu workspace, and a space writer releases neither one nor its stack's
    Given the member key is exchanged through A for token "m"
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through A
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "run", its run token kept as "rt"
    And the worker takes over the run "run" with token "rt" and the key of "csr", checking out "infra", through A
    And the worker reports the run "run" started in INITIALIZING through B
    And terraform "init" of the run "run" exits 0
    And the worker reports the run "run" succeeded in INITIALIZING through A
    And the relay holds the state backend's unlocks
    And terraform "plan" of the run "run" starts as "plan"
    And the relay holds 1 unlock of the state of the run "run"
    And token "h" sends "POST /api/v1/tofu-workspaces/search" with '{}' through B
    And the answer's Tofu workspace "default" names "ws"
    When token "m" sends "POST /api/v1/tofu-workspaces/search" with '{}' through A
    Then the answer's Tofu workspaces are ""
    When token "m" sends "POST /api/v1/tofu-workspaces/search/suggestions" with '{"fields":["name"]}' through B
    Then the answer suggests the name values "" of 0 Tofu workspaces
    When token "m" sends "POST /api/v1/tofu-workspaces/{ws}/unlock" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/stacks/infra/tofu-workspaces/unlock" through B
    Then it answers a 404 problem "not-found"
    Given the member key is space-writer on the space "legacy"
    When token "m" sends "POST /api/v1/tofu-workspaces/search" with '{}' through A
    Then the answer's Tofu workspaces are "default locked by the home key"
    When token "m" sends "POST /api/v1/tofu-workspaces/{ws}/unlock" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_WORKSPACE_UNLOCK"'
    When token "m" sends "POST /api/v1/stacks/infra/tofu-workspaces/unlock" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_WORKSPACE_UNLOCK"'
    And the state of the run "run" is held by the shared locks of the runs "run"
    When the relay releases the state backend's unlocks
    Then the process "plan" exits 0

  @case:failure @op:searchTofuWorkspaces @op:searchTofuWorkspacesSuggestions @op:tofuWorkspaceUnlock @op:tofuWorkspaceUnlockAll
  Scenario: A Tofu workspace or stack the account lacks answers 404, and a search of a field workspaces lack is refused
    When token "h" sends "POST /api/v1/tofu-workspaces/01KACME0000000000000000ZZZ/unlock" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/tofu-workspaces/not-a-workspace/unlock" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks/gone/tofu-workspaces/unlock" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/tofu-workspaces/search" with '{"predicates":[{"field":"owner","constraint":{"stringEquals":["x"]}}]}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/predicates/0/field" as "unknown-field"
    When token "h" sends "POST /api/v1/tofu-workspaces/search" with '{"predicates":[{"field":"isLocked","constraint":{"stringEquals":["yes"]}}]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/predicates/0/constraint/stringEquals" as "rule"
    When token "h" sends "POST /api/v1/tofu-workspaces/search/suggestions" with '{"predicates":[{"field":"owner","constraint":{"stringEquals":["x"]}}]}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/predicates/0/field" as "unknown-field"
    When token "h" sends "POST /api/v1/tofu-workspaces/search" with '{"after":"not-a-cursor"}' through A
    Then it answers a 400 problem "invalid-cursor"

  @tofu @op:stateDownloadUrl @op:stackSearchManagedStateVersions @case:success
  Scenario: Encrypted OpenTofu applies keep their envelope and advance serial in one lineage
    Given token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    And token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"opentofu":{"externalStateAccessEnabled":true}}}' through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "first"
    And token "h" eventually reads the run "first" in the state "READY" through A
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "first", its run token kept as "ft"
    And the worker takes over the run "first" with token "ft" and the key of "csr", checking out "infra", through A
    And the tool environment of the run "first" sets "TF_ENCRYPTION" from fixture "test/fixtures/vendor-documents/opentofu/encryption.hcl"
    And the worker reports the run "first" started in INITIALIZING through B
    Then tofu "init" of the run "first" exits 0
    When the worker reports the run "first" succeeded in INITIALIZING through A
    Then the report's answer is to execute in PLANNING
    And tofu "plan" of the run "first" exits 0
    When the worker reports the run "first" succeeded in PLANNING with the delta '{"add":1,"change":0,"delete":0,"import":0,"outputs":true}' through B
    Then the report's answer is to execute in APPLYING
    And tofu "apply -auto-approve" of the run "first" exits 0
    And the relay passed a POST of the state of the run "first" answered 200
    And the run "first" wrote 1 state version
    And tofu "output -json" of the run "first" exits 0
    And the worker records the last output of the run "first" as its outputs.json through B
    And tofu "state pull" of the run "first" exits 0
    And the current state version is the run "first"'s, with the serial and lineage of its last output
    And the current state version of the run "first" is client-encrypted
    When token "h" sends "POST /api/v1/stacks/infra/state/download-url" with '{}' through A
    Then it answers 200
    And the answer's url is kept as "dl"
    And the URL "dl" read with no credential through B is the current state version of the run "first", holding "encrypted_data"
    And the last answer is an encrypted envelope without "plain-s3cret-0451"
    And no object in the scenario's bucket holds "plain-s3cret-0451", of at least 1 object
    When token "h" searches the state versions of the stack "infra" with '{}' through A
    Then the answer's state versions are "first"
    When the worker reports the run "first" succeeded in APPLYING through A
    Then the report's answer is to exit in FINISHED
    And token "h" eventually reads the run "first" in the state "FINISHED" through B
    When a commit to "main" of "infra" changing "changed"
    And the head of "main" of "infra" is kept as "change"
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{change}"}' through B
    And the answer's id names "second"
    And token "h" eventually reads the run "second" in the state "READY" through A
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "second", its run token kept as "st"
    And the worker takes over the run "second" with token "st" and the key of "csr", checking out "infra", through A
    And the tool environment of the run "second" sets "TF_ENCRYPTION" from fixture "test/fixtures/vendor-documents/opentofu/encryption.hcl"
    And the worker reports the run "second" started in INITIALIZING through B
    Then tofu "init" of the run "second" exits 0
    When the worker reports the run "second" succeeded in INITIALIZING through A
    Then the report's answer is to execute in PLANNING
    And tofu "plan" of the run "second" exits 0
    When the worker reports the run "second" succeeded in PLANNING with the delta '{"add":0,"change":1,"delete":0,"import":0,"outputs":true}' through B
    Then the report's answer is to execute in APPLYING
    And tofu "apply -auto-approve" of the run "second" exits 0
    And the relay passed a POST of the state of the run "second" answered 200
    And the run "second" wrote 1 state version
    And tofu "output -json" of the run "second" exits 0
    And the worker records the last output of the run "second" as its outputs.json through B
    And tofu "state pull" of the run "second" exits 0
    And the current state version is the run "second"'s, with the serial and lineage of its last output
    And the current state version of the run "second" is client-encrypted
    And the version of the run "second" has the next serial and the same lineage as the run "first"
    When the worker reports the run "second" succeeded in APPLYING through A
    Then the report's answer is to exit in FINISHED
    And token "h" eventually reads the run "second" in the state "FINISHED" through B
    When token "h" searches the state versions of the stack "infra" with '{}' through B
    Then the answer's state versions are "second, first"

  @tofu @case:denial
  Scenario: A proposed run and an API key cannot write an encrypted envelope
    Given token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    And token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"opentofu":{"externalStateAccessEnabled":true}}}' through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"PROPOSED"}' through B
    And the answer's id names "proposed"
    And token "h" eventually reads the run "proposed" in the state "READY" through A
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "proposed", its run token kept as "pt"
    And the worker takes over the run "proposed" with token "pt" and the key of "csr", checking out "infra", through A
    And the tool environment of the run "proposed" sets "TF_ENCRYPTION" from fixture "test/fixtures/vendor-documents/opentofu/encryption.hcl"
    And the worker reports the run "proposed" started in INITIALIZING through B
    Then tofu "init" of the run "proposed" exits 0
    When the worker reports the run "proposed" succeeded in INITIALIZING through A
    Then the report's answer is to execute in PLANNING
    And tofu "plan" of the run "proposed" exits 0
    When the fixture "test/fixtures/vendor-documents/opentofu/state-encrypted.json" is posted to the state of the run "proposed" with its run token through A
    Then it answers a 403 problem "forbidden"
    And the problem's detail names "This run may not write the stack's state in its current state."
    And the run "proposed" wrote 0 state versions
    Given the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/roles" with '{"name":"State Readers","actions":["SPACE_READ","STACK_STATE_READ"]}' through B
    And the member key is state-readers on the space "legacy"
    When the fixture "test/fixtures/vendor-documents/opentofu/state-encrypted.json" is posted to the state of the run "proposed" with the member key through B
    Then it answers a 403 problem "forbidden"
    And the problem's detail names "Only the stack's own runs may write its state; any other caller may only read it."
    And the run "proposed" wrote 0 state versions

  @tofu @case:failure
  Scenario: OpenTofu without the encryption key cannot plan or change an encrypted state
    Given token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    And token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"opentofu":{"externalStateAccessEnabled":true}}}' through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "sealed"
    And token "h" eventually reads the run "sealed" in the state "READY" through A
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "sealed", its run token kept as "et"
    And the worker takes over the run "sealed" with token "et" and the key of "csr", checking out "infra", through A
    And the tool environment of the run "sealed" sets "TF_ENCRYPTION" from fixture "test/fixtures/vendor-documents/opentofu/encryption.hcl"
    And the worker reports the run "sealed" started in INITIALIZING through B
    Then tofu "init" of the run "sealed" exits 0
    When the worker reports the run "sealed" succeeded in INITIALIZING through A
    Then the report's answer is to execute in PLANNING
    And tofu "plan" of the run "sealed" exits 0
    When the worker reports the run "sealed" succeeded in PLANNING with the delta '{"add":1,"change":0,"delete":0,"import":0,"outputs":true}' through B
    Then the report's answer is to execute in APPLYING
    And tofu "apply -auto-approve" of the run "sealed" exits 0
    And the relay passed a POST of the state of the run "sealed" answered 200
    And the run "sealed" wrote 1 state version
    And tofu "output -json" of the run "sealed" exits 0
    And the worker records the last output of the run "sealed" as its outputs.json through B
    And tofu "state pull" of the run "sealed" exits 0
    And the current state version is the run "sealed"'s, with the serial and lineage of its last output
    And the current state version of the run "sealed" is client-encrypted
    When the worker reports the run "sealed" succeeded in APPLYING through A
    Then the report's answer is to exit in FINISHED
    And token "h" eventually reads the run "sealed" in the state "FINISHED" through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "unkeyed"
    And token "h" eventually reads the run "unkeyed" in the state "READY" through A
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "unkeyed", its run token kept as "ut"
    And the worker takes over the run "unkeyed" with token "ut" and the key of "csr", checking out "infra", through A
    And the tool environment of the run "unkeyed" sets "TF_ENCRYPTION" from fixture "test/fixtures/vendor-documents/opentofu/encryption.hcl"
    And the worker reports the run "unkeyed" started in INITIALIZING through B
    Then tofu "init" of the run "unkeyed" exits 0
    And the tool environment of the run "unkeyed" unsets "TF_ENCRYPTION"
    When the worker reports the run "unkeyed" succeeded in INITIALIZING through A
    Then the report's answer is to execute in PLANNING
    And tofu "plan" of the run "unkeyed" exits 1
    And the last standard error of the run "unkeyed" holds 'This state file is encrypted and can not be read without an encryption configuration'
    And the run "unkeyed" wrote 0 state versions
    And the current state version is the run "sealed"'s, with the serial and lineage of its last output
    When the worker reports the run "unkeyed" failed in PLANNING through B
    Then the report's answer is to exit in FAILED
