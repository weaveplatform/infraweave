@postgres @objectstore @vcs @docker
Feature: A state file is imported at a stack's creation or into an existing stack
  A caller uploads a state file through a signed upload URL, then names
  its object ID either as stackCreate's stackObjectID, so the new stack's
  default workspace starts from it, or as stackManagedStateImport's state,
  so an existing stack's takes it as its new current version. Either way
  infraweave reseals the file into its states store as a version marked
  imported by the caller, with no run, deletes the upload and records the
  import on the audit trail. An import needs STACK_MANAGED_STATE_IMPORT on
  the stack's space and a stack whose state infraweave keeps, as external
  state access does; one after the stack's creation also needs the
  caller's lock on the stack and no tracked run, task or destroy run under
  way, though a proposed run, which writes no state, does not count. The
  upload must be the caller's own and
  a JSON state of format version 4, or OpenTofu's client-encrypted
  envelope, which infraweave keeps unchanged with any serial and lineage
  it carries. The scenario is the
  worker of stack infra's runs, as in the backend feature: a tracked run
  applies its configuration, a terraform_data resource and outputs, and
  the tool pulls the state it wrote, which the scenario uploads and
  imports into other stacks, whose tracked runs then plan no change
  against it. The home key is space-admin on the account's top space; the
  member key holds only the bindings a scenario gives it.
  ref: decision 0010
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = "one"\n}\n\noutput "input" {\n  value = terraform_data.this.output\n}\n\noutput "token" {\n  value     = "plain-1mp0rt-0509"\n  sensitive = true\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true},"manageState":true}' through B
    And the launcher "w" registers with the key of "csr" and the certificate "cert" through A

  @op:stackCreate @op:stackManagedStateImport @case:success
  Scenario Outline: A state one stack's apply wrote, imported into a new stack and into an existing one, plans no change in either
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
    When the worker reports the run "run" succeeded in PLANNING with the delta '{"add":1,"change":0,"delete":0,"import":0,"outputs":true}' through B
    Then the report's answer is to execute in APPLYING
    And <tool> "apply -auto-approve" of the run "run" exits 0
    And <tool> "output -json" of the run "run" exits 0
    And the worker records the last output of the run "run" as its outputs.json through B
    And <tool> "state pull" of the run "run" exits 0
    When the worker reports the run "run" succeeded in APPLYING through A
    Then the report's answer is to exit in FINISHED
    When token "h" sends "POST /api/v1/state/upload-url" with '{}' through A
    And the answer's url is kept as "up"
    And the answer's objectId names "obj"
    And the last output of the run "run" is put with no credential at the URL "up" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Copy","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}"},"manageState":true,"stackObjectID":"{obj}"}' through B
    Then it answers 201
    And the current state version of the stack "copy" is imported by the home key, with the serial and lineage of the last output of the run "run"
    And the upload of the URL "up" is gone from the uploads store
    And the audit trail holds 1 import into the stack "copy" by the home key
    When token "h" sends "POST /api/v1/stacks/copy/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "copied"
    And token "h" eventually reads the run "copied" in the state "READY" through B
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through A
    And the answer assigns the run "copied", its run token kept as "ct"
    And the worker takes over the run "copied" with token "ct" and the key of "csr", checking out "infra", through B
    And the worker reports the run "copied" started in INITIALIZING through A
    Then <tool> "init" of the run "copied" exits 0
    When the worker reports the run "copied" succeeded in INITIALIZING through B
    Then the report's answer is to execute in PLANNING
    And <tool> "plan -detailed-exitcode" of the run "copied" exits 0
    When the worker reports the run "copied" succeeded in PLANNING with the delta '{"add":0,"change":0,"delete":0,"import":0,"outputs":false}' through A
    Then the report's answer is to exit in FINISHED
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Later","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}"},"manageState":true}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/stacks/later/lock" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/state/upload-url" with '{}' through B
    And the answer's url is kept as "again"
    And the answer's objectId names "obj2"
    And the last output of the run "run" is put with no credential at the URL "again" through A
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/later/managed-state-import" with '{"state":"{obj2}"}' through A
    Then the answer is 'true'
    And the current state version of the stack "later" is imported by the home key, with the serial and lineage of the last output of the run "run"
    And the upload of the URL "again" is gone from the uploads store
    And the audit trail holds 1 import into the stack "later" by the home key
    When token "h" sends "POST /api/v1/stacks/later/unlock" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/later/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "later"
    And token "h" eventually reads the run "later" in the state "READY" through A
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "later", its run token kept as "lt"
    And the worker takes over the run "later" with token "lt" and the key of "csr", checking out "infra", through A
    And the worker reports the run "later" started in INITIALIZING through B
    Then <tool> "init" of the run "later" exits 0
    When the worker reports the run "later" succeeded in INITIALIZING through A
    Then the report's answer is to execute in PLANNING
    And <tool> "plan -detailed-exitcode" of the run "later" exits 0
    When the worker reports the run "later" succeeded in PLANNING with the delta '{"add":0,"change":0,"delete":0,"import":0,"outputs":false}' through B
    Then the report's answer is to exit in FINISHED
    And no object in the scenario's bucket holds "plain-1mp0rt-0509", of at least 3 objects

    @terraform
    Examples: Terraform
      | tool      |
      | terraform |

    @tofu
    Examples: OpenTofu
      | tool |
      | tofu |

  @tofu @op:stackCreate @op:stackManagedStateImport @case:success
  Scenario: An encrypted run's downloaded envelope is imported with its metadata and plans no change
    Given token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    And token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"opentofu":{"externalStateAccessEnabled":true}}}' through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "encrypted"
    And token "h" eventually reads the run "encrypted" in the state "READY" through A
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "encrypted", its run token kept as "et"
    And the worker takes over the run "encrypted" with token "et" and the key of "csr", checking out "infra", through A
    And the tool environment of the run "encrypted" sets "TF_ENCRYPTION" from fixture "test/fixtures/vendor-documents/opentofu/encryption.hcl"
    And the worker reports the run "encrypted" started in INITIALIZING through B
    Then tofu "init" of the run "encrypted" exits 0
    When the worker reports the run "encrypted" succeeded in INITIALIZING through A
    Then the report's answer is to execute in PLANNING
    And tofu "plan" of the run "encrypted" exits 0
    When the worker reports the run "encrypted" succeeded in PLANNING with the delta '{"add":1,"change":0,"delete":0,"import":0,"outputs":true}' through B
    Then the report's answer is to execute in APPLYING
    And tofu "apply -auto-approve" of the run "encrypted" exits 0
    And the relay passed a POST of the state of the run "encrypted" answered 200
    And the run "encrypted" wrote 1 state version
    And tofu "output -json" of the run "encrypted" exits 0
    And the worker records the last output of the run "encrypted" as its outputs.json through B
    And tofu "state pull" of the run "encrypted" exits 0
    And the current state version is the run "encrypted"'s, with the serial and lineage of its last output
    And the current state version of the run "encrypted" is client-encrypted
    When the worker reports the run "encrypted" succeeded in APPLYING through A
    Then the report's answer is to exit in FINISHED
    And token "h" eventually reads the run "encrypted" in the state "FINISHED" through B
    When token "h" sends "POST /api/v1/stacks/infra/state/download-url" with '{}' through A
    And the answer's url is kept as "encrypted"
    And the URL "encrypted" read with no credential through B is the current state version of the run "encrypted", holding "encrypted_data"
    And the last answer is an encrypted envelope without "plain-1mp0rt-0509"
    When token "h" sends "POST /api/v1/state/upload-url" with '{}' through A
    And the answer's url is kept as "up"
    And the answer's objectId names "obj"
    And the state downloaded from the URL "encrypted" is put with no credential at the URL "up" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Sealed","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}}},"manageState":true,"stackObjectID":"{obj}"}' through A
    Then it answers 201
    And the current state version of the stack "sealed" is imported by the home key, client-encrypted, with the serial and lineage of the last output of the run "encrypted"
    And the upload of the URL "up" is gone from the uploads store
    When token "h" sends "POST /api/v1/stacks/sealed/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "copied"
    And token "h" eventually reads the run "copied" in the state "READY" through A
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "copied", its run token kept as "ct"
    And the worker takes over the run "copied" with token "ct" and the key of "csr", checking out "infra", through A
    And the tool environment of the run "copied" sets "TF_ENCRYPTION" from fixture "test/fixtures/vendor-documents/opentofu/encryption.hcl"
    And the worker reports the run "copied" started in INITIALIZING through B
    Then tofu "init" of the run "copied" exits 0
    When the worker reports the run "copied" succeeded in INITIALIZING through A
    Then the report's answer is to execute in PLANNING
    And tofu "plan -detailed-exitcode" of the run "copied" exits 0
    When the worker reports the run "copied" succeeded in PLANNING with the delta '{"add":0,"change":0,"delete":0,"import":0,"outputs":false}' through A
    Then the report's answer is to exit in FINISHED
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Later","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}}},"manageState":true}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/stacks/later/lock" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/state/upload-url" with '{}' through A
    And the answer's url is kept as "existing"
    And the answer's objectId names "existing"
    And the state downloaded from the URL "encrypted" is put with no credential at the URL "existing" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/later/managed-state-import" with '{"state":"{existing}"}' through A
    Then the answer is 'true'
    And the current state version of the stack "later" is imported by the home key, client-encrypted, with the serial and lineage of the last output of the run "encrypted"
    And the upload of the URL "existing" is gone from the uploads store
    When token "h" sends "POST /api/v1/stacks/later/unlock" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/later/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "later"
    And token "h" eventually reads the run "later" in the state "READY" through A
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "later", its run token kept as "lt"
    And the worker takes over the run "later" with token "lt" and the key of "csr", checking out "infra", through A
    And the tool environment of the run "later" sets "TF_ENCRYPTION" from fixture "test/fixtures/vendor-documents/opentofu/encryption.hcl"
    And the worker reports the run "later" started in INITIALIZING through B
    Then tofu "init" of the run "later" exits 0
    When the worker reports the run "later" succeeded in INITIALIZING through A
    Then the report's answer is to execute in PLANNING
    And tofu "plan -detailed-exitcode" of the run "later" exits 0
    When the worker reports the run "later" succeeded in PLANNING with the delta '{"add":0,"change":0,"delete":0,"import":0,"outputs":false}' through B
    Then the report's answer is to exit in FINISHED
    When token "h" sends "POST /api/v1/stacks/sealed/lock" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/sealed/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    Then it answers 200
    When token "h" sends "POST /api/v1/state/upload-url" with '{}' through B
    And the answer's url is kept as "again"
    And the answer's objectId names "obj2"
    And '{"meta":{},"encrypted_data":"c2VhbGVkLXR3bw==","encryption_version":"v0"}' is put with no credential at the URL "again" through A
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/sealed/managed-state-import" with '{"state":"{obj2}"}' through B
    Then the answer is 'true'
    And the current state version of the stack "sealed" is imported by the home key, client-encrypted
    And the stack "sealed" has 2 imported state versions
    And the audit trail holds 2 imports into the stack "sealed" by the home key

  @op:stackCreate @op:stackManagedStateImport @case:denial
  Scenario Outline: A caller without STACK_MANAGED_STATE_IMPORT creates no stack from an upload and imports into none
    Given token "h" sends "POST /api/v1/state/upload-url" with '{}' through A
    And the answer's url is kept as "up"
    And the answer's objectId names "obj"
    And the fixture "<fixture>" is put with no credential at the URL "up" through B
    And token "h" sends "POST /api/v1/roles" with '{"name":"Stack Creators","actions":["SPACE_READ","STACK_CREATE","STACK_LOCK"]}' through A
    And the member key is stack-creators on the space "legacy"
    When token "m" sends "POST /api/v1/stacks" with '{"input":{"name":"Copy","repository":"infra","branch":"main","space":"legacy"},"manageState":true,"stackObjectID":"{obj}"}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_MANAGED_STATE_IMPORT"'
    When token "h" sends "GET /api/v1/stacks/copy" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/stacks/infra/lock" through A
    Then it answers 200
    When token "m" sends "POST /api/v1/stacks/infra/managed-state-import" with '{"state":"{obj}"}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_MANAGED_STATE_IMPORT"'
    And the upload of the URL "up" is one sealed object in the uploads store

    Examples:
      | fixture                                                        |
      | test/fixtures/vendor-documents/opentofu/state.json               |
      | test/fixtures/vendor-documents/opentofu/state-encrypted.json     |

  @op:stackCreate @case:failure
  Scenario: A stack is not created from an upload that is missing or used, into a stack without managed state, or that is no state, nor with external state access but no managed state
    Given token "h" sends "POST /api/v1/state/upload-url" with '{}' through A
    And the answer's url is kept as "up"
    And the answer's objectId names "obj"
    And '{"version":4,"serial":1,"lineage":"l","outputs":{},"resources":[]}' is put with no credential at the URL "up" through B
    And token "h" sends "POST /api/v1/state/upload-url" with '{}' through B
    And the answer's url is kept as "old"
    And the answer's objectId names "v3"
    And '{"version":3,"serial":1,"lineage":"l","modules":[]}' is put with no credential at the URL "old" through A
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Copy","repository":"infra","branch":"main","space":"legacy"},"manageState":true,"stackObjectID":"01KACME0000000000000000ZZZ"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/stackObjectID" as "not-found"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Copy","repository":"infra","branch":"main","space":"legacy"},"manageState":false,"stackObjectID":"{obj}"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/stackObjectID" as "rule"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Copy","repository":"infra","branch":"main","space":"legacy","vendorConfig":{"terraform":{"externalStateAccessEnabled":true}}},"manageState":false}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/input/vendorConfig/terraform/externalStateAccessEnabled" as "rule"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Copy","repository":"infra","branch":"main","space":"legacy"},"manageState":true,"stackObjectID":"{v3}"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/stackObjectID" as "rule"
    When token "h" sends "GET /api/v1/stacks/copy" through B
    Then it answers a 404 problem "not-found"
    And the upload of the URL "up" is one sealed object in the uploads store
    And the upload of the URL "old" is one sealed object in the uploads store
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Copy","repository":"infra","branch":"main","space":"legacy"},"manageState":true,"stackObjectID":"{obj}"}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Again","repository":"infra","branch":"main","space":"legacy"},"manageState":true,"stackObjectID":"{obj}"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/stackObjectID" as "not-found"
    When token "h" sends "GET /api/v1/stacks/again" through A
    Then it answers a 404 problem "not-found"

  @op:stackManagedStateImport @case:failure
  Scenario: An import is refused into a stack without managed state, unlocked, locked by another or with a tracked run or a task under way, and of an upload that is missing, another's or no state
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Plain","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through A
    And token "h" sends "POST /api/v1/state/upload-url" with '{}' through B
    And the answer's url is kept as "up"
    And the answer's objectId names "obj"
    And '{"version":4,"serial":1,"lineage":"l","outputs":{},"resources":[]}' is put with no credential at the URL "up" through A
    And token "h" sends "POST /api/v1/roles" with '{"name":"Importers","actions":["SPACE_READ","STACK_LOCK","STACK_UNLOCK","STACK_MANAGED_STATE_IMPORT"]}' through B
    And the member key is importers on the space "legacy"
    When token "h" sends "POST /api/v1/stacks/plain/managed-state-import" with '{"state":"{obj}"}' through A
    Then it answers a 409 problem "invalid-state"
    And the problem's detail names "not managed by infraweave"
    When token "h" sends "POST /api/v1/stacks/infra/managed-state-import" with '{"state":"{obj}"}' through B
    Then it answers a 409 problem "invalid-state"
    And the problem's detail names "not locked"
    When token "m" sends "POST /api/v1/stacks/infra/lock" through A
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/managed-state-import" with '{"state":"{obj}"}' through B
    Then it answers a 409 problem "invalid-state"
    And the problem's detail names "locked by 01KACME000000000000000000A"
    When token "m" sends "POST /api/v1/stacks/infra/unlock" through A
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/lock" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/managed-state-import" with '{"state":"01KACME0000000000000000ZZZ"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/state" as "not-found"
    When token "m" sends "POST /api/v1/state/upload-url" with '{}' through B
    And the answer's url is kept as "theirs"
    And the answer's objectId names "theirs"
    And '{"version":4,"serial":1,"lineage":"l","outputs":{},"resources":[]}' is put with no credential at the URL "theirs" through A
    And token "h" sends "POST /api/v1/stacks/infra/managed-state-import" with '{"state":"{theirs}"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/state" as "not-found"
    When token "h" sends "POST /api/v1/state/upload-url" with '{}' through A
    And the answer's url is kept as "bad"
    And the answer's objectId names "bad"
    And '{"serial":1,"lineage":"l"}' is put with no credential at the URL "bad" through B
    And token "h" sends "POST /api/v1/stacks/infra/managed-state-import" with '{"state":"{bad}"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/state" as "rule"
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "pending"
    And token "h" sends "POST /api/v1/stacks/infra/managed-state-import" with '{"state":"{obj}"}' through A
    Then it answers a 409 problem "invalid-state"
    And the problem's detail names the run "pending"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{pending}/cancel" with '{}' through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"echo task"}' through A
    And the answer's id names "task"
    And token "h" sends "POST /api/v1/stacks/infra/managed-state-import" with '{"state":"{obj}"}' through B
    Then it answers a 409 problem "invalid-state"
    And the problem's detail names the run "task"
    And the upload of the URL "up" is one sealed object in the uploads store
    And the upload of the URL "theirs" is one sealed object in the uploads store
    And the stack "infra" has 0 imported state versions

  @op:stackCreate @op:stackManagedStateImport @case:failure
  Scenario Outline: An incomplete encrypted envelope or empty object is refused at both import entry points
    When token "h" sends "POST /api/v1/state/upload-url" with '{}' through A
    And the answer's url is kept as "invalid"
    And the answer's objectId names "invalid"
    And '<body>' is put with no credential at the URL "invalid" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Invalid","repository":"infra","branch":"main","space":"legacy"},"manageState":true,"stackObjectID":"{invalid}"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/stackObjectID" as "rule"
    When token "h" sends "POST /api/v1/stacks/infra/lock" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/managed-state-import" with '{"state":"{invalid}"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/state" as "rule"
    And the upload of the URL "invalid" is one sealed object in the uploads store

    Examples:
      | body                                                    |
      | {"serial":1,"lineage":"l","encrypted_data":"eA=="}       |
      | {}                                                      |
