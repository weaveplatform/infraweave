@postgres @objectstore @vcs @docker
Feature: A stack's state is rolled back as a new version, and its versions are searched
  stackManagedStateRollback copies an earlier version of a stack's
  default workspace into a new current version: infraweave unseals the
  target, reseals it into an object of its own, and records a version
  that keeps the target's run, serial, lineage and import, marked as the
  caller's rollback from the target, on the audit trail too. A rollback
  needs STACK_MANAGED_STATE_ROLLBACK on the stack's space, a stack whose
  state infraweave keeps, the caller's lock on it and no tracked run,
  task or destroy run under way, as an import does; it is refused to the
  current version, and to a version the stack lacks, which is how a
  version retention deleted answers. The stack answers isStateRollback
  until a run or an import writes a newer version, so a run whose plan
  finds nothing to change leaves it set.
  stackSearchManagedStateVersions lists the default workspace's versions
  newest first, by when each was written, its run, whether it is a
  rollback and whether it is an import, and pages with a cursor; a
  rollback names the run of the version it copies. The scenario is the
  worker of stack infra's runs, as in the backend feature: its
  configuration keeps a terraform_data resource and drops another once
  the commit adds the file drop. The home key is space-admin on the
  account's top space; the member key holds only the bindings a scenario
  gives it.
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
      | main.tf | resource "terraform_data" "kept" {\n  input = "one"\n}\n\nresource "terraform_data" "dropped" {\n  count = fileexists("${path.module}/drop") ? 0 : 1\n  input = "two"\n}\n\noutput "token" {\n  value     = "plain-r0llb4ck-s3cret"\n  sensitive = true\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true},"manageState":true}' through B
    And the launcher "w" registers with the key of "csr" and the certificate "cert" through A

  @op:stackManagedStateRollback @op:stackSearchManagedStateVersions @case:success
  Scenario Outline: A rollback to the first of two applies restores what the second removed, and the stack is rolled back until a run writes
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "first"
    And token "h" eventually reads the run "first" in the state "READY" through A
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "first", its run token kept as "ft"
    And the worker takes over the run "first" with token "ft" and the key of "csr", checking out "infra", through A
    And the worker reports the run "first" started in INITIALIZING through B
    Then <tool> "init" of the run "first" exits 0
    When the worker reports the run "first" succeeded in INITIALIZING through A
    Then the report's answer is to execute in PLANNING
    And <tool> "plan" of the run "first" exits 0
    When the worker reports the run "first" succeeded in PLANNING with the delta '{"add":2,"change":0,"delete":0,"import":0,"outputs":true}' through B
    Then the report's answer is to execute in APPLYING
    And <tool> "apply -auto-approve" of the run "first" exits 0
    And <tool> "output -json" of the run "first" exits 0
    And the worker records the last output of the run "first" as its outputs.json through B
    When the worker reports the run "first" succeeded in APPLYING through A
    Then the report's answer is to exit in FINISHED
    And the run "first" wrote 1 state version
    When a commit to "main" of "infra" changing "drop"
    And the head of "main" of "infra" is kept as "dropping"
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{dropping}"}' through A
    And the answer's id names "second"
    And token "h" eventually reads the run "second" in the state "READY" through B
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through A
    And the answer assigns the run "second", its run token kept as "st"
    And the worker takes over the run "second" with token "st" and the key of "csr", checking out "infra", through B
    And the worker reports the run "second" started in INITIALIZING through A
    Then <tool> "init" of the run "second" exits 0
    When the worker reports the run "second" succeeded in INITIALIZING through B
    Then the report's answer is to execute in PLANNING
    And <tool> "plan" of the run "second" exits 0
    And the last output of the run "second" holds '0 to add, 0 to change, 1 to destroy'
    When the worker reports the run "second" succeeded in PLANNING with the delta '{"add":0,"change":0,"delete":1,"import":0,"outputs":false}' through A
    Then the report's answer is to execute in APPLYING
    And <tool> "apply -auto-approve" of the run "second" exits 0
    And <tool> "output -json" of the run "second" exits 0
    And the worker records the last output of the run "second" as its outputs.json through A
    When the worker reports the run "second" succeeded in APPLYING through B
    Then the report's answer is to exit in FINISHED
    And the run "second" wrote 1 state version
    When token "h" searches the state versions of the stack "infra" with '{}' through A
    Then the answer's state versions are "second, first"
    And the answer's 2nd state version names "v1"
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then the answer's isStateRollback is 'false'
    When token "h" sends "POST /api/v1/stacks/infra/lock" through A
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/managed-state-rollback" with '{"managedStateVersionId":"{v1}"}' through B
    Then the answer is a rollback by the home key to a version of the run "first"
    And the current state version of the stack "infra" is a rollback to "v1"
    And the audit trail holds 1 rollback of the stack "infra" by the home key
    And the run "first" wrote 1 state version
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then the answer's isStateRollback is 'true'
    When token "h" sends "POST /api/v1/stacks/infra/unlock" through B
    Then it answers 200
    When token "h" searches the state versions of the stack "infra" with '{"predicates":[{"field":"rollback","constraint":{"booleanEquals":[true]}}]}' through B
    Then the answer's state versions are "first rolled back"
    When token "h" searches the state versions of the stack "infra" with '{"predicates":[{"field":"run","constraint":{"stringEquals":["{first}"]}}]}' through A
    Then the answer's state versions are "first rolled back, first"
    When token "h" searches the state versions of the stack "infra" with '{"first":2}' through B
    Then the answer's state versions are "first rolled back, second"
    And the answer's endCursor is kept as "page"
    When token "h" searches the state versions of the stack "infra" with '{"first":2,"after":"{page}"}' through A
    Then the answer's state versions are "first"
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "replan"
    And token "h" eventually reads the run "replan" in the state "READY" through A
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "replan", its run token kept as "rp"
    And the worker takes over the run "replan" with token "rp" and the key of "csr", checking out "infra", through A
    And the worker reports the run "replan" started in INITIALIZING through B
    Then <tool> "init" of the run "replan" exits 0
    When the worker reports the run "replan" succeeded in INITIALIZING through A
    Then the report's answer is to execute in PLANNING
    And <tool> "plan -detailed-exitcode" of the run "replan" exits 0
    And the last output of the run "replan" holds 'No changes.'
    When the worker reports the run "replan" succeeded in PLANNING with the delta '{"add":0,"change":0,"delete":0,"import":0,"outputs":false}' through B
    Then the report's answer is to exit in FINISHED
    And the run "replan" wrote 0 state versions
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then the answer's isStateRollback is 'true'
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{dropping}"}' through A
    And the answer's id names "third"
    And token "h" eventually reads the run "third" in the state "READY" through B
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through A
    And the answer assigns the run "third", its run token kept as "tt"
    And the worker takes over the run "third" with token "tt" and the key of "csr", checking out "infra", through B
    And the worker reports the run "third" started in INITIALIZING through A
    Then <tool> "init" of the run "third" exits 0
    When the worker reports the run "third" succeeded in INITIALIZING through B
    Then the report's answer is to execute in PLANNING
    And <tool> "plan -detailed-exitcode" of the run "third" exits 2
    And the last output of the run "third" holds '0 to add, 0 to change, 1 to destroy'
    When the worker reports the run "third" succeeded in PLANNING with the delta '{"add":0,"change":0,"delete":1,"import":0,"outputs":false}' through A
    Then the report's answer is to execute in APPLYING
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then the answer's isStateRollback is 'true'
    And <tool> "apply -auto-approve" of the run "third" exits 0
    And <tool> "output -json" of the run "third" exits 0
    And the worker records the last output of the run "third" as its outputs.json through A
    And the run "third" wrote 1 state version
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then the answer's isStateRollback is 'false'
    When the worker reports the run "third" succeeded in APPLYING through B
    Then the report's answer is to exit in FINISHED
    When token "h" searches the state versions of the stack "infra" with '{}' through B
    Then the answer's state versions are "third, first rolled back, second, first"
    And no object in the scenario's bucket holds "plain-r0llb4ck-s3cret", of at least 4 objects

    @terraform
    Examples: Terraform
      | tool      |
      | terraform |

    @tofu
    Examples: OpenTofu
      | tool |
      | tofu |

  @op:stackManagedStateRollback @op:stackSearchManagedStateVersions @case:success
  Scenario: A rollback to an imported version keeps its import, and the stack is rolled back until the next import
    Given token "h" sends "POST /api/v1/state/upload-url" with '{}' through A
    And the answer's url is kept as "one"
    And the answer's objectId names "one"
    And '{"version":4,"serial":1,"lineage":"l","outputs":{},"resources":[]}' is put with no credential at the URL "one" through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Copy","repository":"infra","branch":"main","space":"legacy"},"manageState":true,"stackObjectID":"{one}"}' through A
    And token "h" sends "POST /api/v1/stacks/copy/lock" through B
    And token "h" sends "POST /api/v1/state/upload-url" with '{}' through A
    And the answer's url is kept as "two"
    And the answer's objectId names "two"
    And '{"version":4,"serial":2,"lineage":"l","outputs":{},"resources":[]}' is put with no credential at the URL "two" through B
    And token "h" sends "POST /api/v1/stacks/copy/managed-state-import" with '{"state":"{two}"}' through A
    When token "h" searches the state versions of the stack "copy" with '{"predicates":[{"field":"imported","constraint":{"booleanEquals":[true]}}]}' through B
    Then the answer's state versions are "imported, imported"
    And the answer's 2nd state version names "v1"
    When token "h" sends "POST /api/v1/stacks/copy/managed-state-rollback" with '{"managedStateVersionId":"{v1}"}' through A
    Then the answer is a rollback by the home key to an import by the home key
    And the current state version of the stack "copy" is a rollback to "v1"
    And the audit trail holds 1 rollback of the stack "copy" by the home key
    When token "h" searches the state versions of the stack "copy" with '{"predicates":[{"field":"rollback","constraint":{"booleanEquals":[false]}}]}' through B
    Then the answer's state versions are "imported, imported"
    When token "h" searches the state versions of the stack "copy" with '{"predicates":[{"field":"imported","constraint":{"booleanEquals":[true]}}]}' through A
    Then the answer's state versions are "imported rolled back, imported, imported"
    When token "h" sends "GET /api/v1/stacks/copy" through B
    Then the answer's isStateRollback is 'true'
    When token "h" sends "POST /api/v1/state/upload-url" with '{}' through A
    And the answer's url is kept as "three"
    And the answer's objectId names "three"
    And '{"version":4,"serial":3,"lineage":"l","outputs":{},"resources":[]}' is put with no credential at the URL "three" through B
    And token "h" sends "POST /api/v1/stacks/copy/managed-state-import" with '{"state":"{three}"}' through A
    Then the answer is 'true'
    When token "h" sends "GET /api/v1/stacks/copy" through B
    Then the answer's isStateRollback is 'false'

  @op:stackManagedStateRollback @op:stackSearchManagedStateVersions @case:denial
  Scenario: A caller who cannot read a stack searches none of its versions, and one without STACK_MANAGED_STATE_ROLLBACK rolls back none
    When token "m" searches the state versions of the stack "infra" with '{}' through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/roles" with '{"name":"Lockers","actions":["SPACE_READ","STACK_LOCK","STACK_UNLOCK"]}' through B
    And the member key is lockers on the space "legacy"
    When token "m" searches the state versions of the stack "infra" with '{}' through B
    Then the answer's state versions are ""
    When token "m" sends "POST /api/v1/stacks/infra/lock" through A
    Then it answers 200
    When token "m" sends "POST /api/v1/stacks/infra/managed-state-rollback" with '{"managedStateVersionId":"01KACME0000000000000000ZZZ"}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_MANAGED_STATE_ROLLBACK"'
    And the audit trail holds 0 rollbacks of the stack "infra" by the member key

  @op:stackManagedStateRollback @case:failure
  Scenario: A rollback is refused of a stack without managed state, to its current version or one it lacks, with a body it does not take, unlocked, locked by another, or with a tracked run under way
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Plain","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through A
    And token "h" sends "POST /api/v1/state/upload-url" with '{}' through B
    And the answer's url is kept as "up"
    And the answer's objectId names "obj"
    And '{"version":4,"serial":1,"lineage":"l","outputs":{},"resources":[]}' is put with no credential at the URL "up" through A
    And token "h" sends "POST /api/v1/stacks/infra/lock" through B
    And token "h" sends "POST /api/v1/stacks/infra/managed-state-import" with '{"state":"{obj}"}' through A
    And token "h" searches the state versions of the stack "infra" with '{}' through B
    And the answer's 1st state version names "current"
    And token "h" sends "POST /api/v1/roles" with '{"name":"Rollers","actions":["SPACE_READ","STACK_LOCK","STACK_UNLOCK","STACK_MANAGED_STATE_ROLLBACK"]}' through A
    And the member key is rollers on the space "legacy"
    When token "h" sends "POST /api/v1/stacks/plain/managed-state-rollback" with '{"managedStateVersionId":"{current}"}' through B
    Then it answers a 409 problem "invalid-state"
    And the problem's detail names "not managed by infraweave"
    When token "h" sends "POST /api/v1/stacks/infra/managed-state-rollback" with '{"managedStateVersionId":"{current}"}' through A
    Then it answers a 409 problem "invalid-state"
    And the problem's detail names "current version"
    When token "h" sends "POST /api/v1/stacks/infra/managed-state-rollback" with '{"managedStateVersionId":"01KACME0000000000000000ZZZ"}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks/infra/managed-state-rollback" with '{"managedStateVersionId":"{current}","colour":"red"}' through A
    Then it answers a 400 problem "invalid-request"
    And the problem refuses the field "/colour" as "unknown-field"
    When token "h" sends "POST /api/v1/stacks/infra/unlock" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/managed-state-rollback" with '{"managedStateVersionId":"{current}"}' through A
    Then it answers a 409 problem "invalid-state"
    And the problem's detail names "not locked"
    When token "m" sends "POST /api/v1/stacks/infra/lock" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/managed-state-rollback" with '{"managedStateVersionId":"{current}"}' through A
    Then it answers a 409 problem "invalid-state"
    And the problem's detail names "locked by 01KACME000000000000000000A"
    When token "m" sends "POST /api/v1/stacks/infra/unlock" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/lock" through A
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "pending"
    And token "h" sends "POST /api/v1/stacks/infra/managed-state-rollback" with '{"managedStateVersionId":"{current}"}' through A
    Then it answers a 409 problem "invalid-state"
    And the problem's detail names the run "pending"
    And the stack "infra" has 1 imported state version
    And the audit trail holds 0 rollbacks of the stack "infra" by the home key

  @op:stackSearchManagedStateVersions @case:failure
  Scenario: A search of a field versions lack, a constraint its field does not take, an altered cursor, or a stack that does not exist is refused
    When token "h" searches the state versions of the stack "infra" with '{"predicates":[{"field":"owner","constraint":{"stringEquals":["x"]}}]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/predicates/0/field" as "unknown-field"
    When token "h" searches the state versions of the stack "infra" with '{"predicates":[{"field":"rollback","constraint":{"stringEquals":["x"]}}]}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/predicates/0/constraint/stringEquals" as "rule"
    When token "h" searches the state versions of the stack "infra" with '{"after":"not-a-cursor"}' through A
    Then it answers a 400 problem "invalid-cursor"
    When token "h" searches the state versions of the stack "gone" with '{}' through B
    Then it answers a 404 problem "not-found"
