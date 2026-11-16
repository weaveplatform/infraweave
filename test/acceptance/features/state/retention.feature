@postgres @objectstore @vcs @docker
Feature: A stack's non-current state versions are deleted once their retention has passed
  The daily state.retention task deletes each state version that stopped
  being current more than state.noncurrent_retention ago, which is when
  the next version of its workspace was written: its row first, and its
  object once that commits. It never deletes a workspace's current
  version, or a version a run that has not ended wrote, which a pass after
  the run ends deletes. A deleted version leaves the version search, and
  a rollback to it answers 404, as one to a version the stack never had
  does. The servers keep a non-current version for an hour. A real
  server's clock cannot be moved, so time passes for a stack's versions by
  moving their creation back, and the task then falls due. The scenario
  is the worker of stack infra's runs, as in the backend feature, and the
  home key is space-admin on the account's top space.
  ref: decisions 0010, 0011 and 0016, DV-036

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database, with the settings '{"state":{"noncurrent_retention":"1h"}}'
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "kept" {\n  input = "one"\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true},"manageState":true}' through B
    And the launcher "w" registers with the key of "csr" and the certificate "cert" through A

  @terraform
  Scenario: Retention deletes two imports and then a run's first apply once the run ends, keeping the current version and one replaced within the hour, and a rollback to a deleted version answers 404
    Given token "h" sends "POST /api/v1/stacks/infra/lock" through A
    And token "h" sends "POST /api/v1/state/upload-url" with '{}' through B
    And the answer's url is kept as "one"
    And the answer's objectId names "one"
    And '{"version":4,"terraform_version":"1.5.7","serial":1,"lineage":"l","outputs":{},"resources":[]}' is put with no credential at the URL "one" through A
    And token "h" sends "POST /api/v1/stacks/infra/managed-state-import" with '{"state":"{one}"}' through B
    And the answer is 'true'
    And token "h" sends "POST /api/v1/state/upload-url" with '{}' through A
    And the answer's url is kept as "two"
    And the answer's objectId names "two"
    And '{"version":4,"terraform_version":"1.5.7","serial":2,"lineage":"l","outputs":{},"resources":[]}' is put with no credential at the URL "two" through B
    And token "h" sends "POST /api/v1/stacks/infra/managed-state-import" with '{"state":"{two}"}' through A
    And the answer is 'true'
    And token "h" sends "POST /api/v1/stacks/infra/unlock" through B
    And token "h" searches the state versions of the stack "infra" with '{}' through A
    And the answer's state versions are "imported, imported"
    And the answer's 1st state version names "v2"
    And the answer's 2nd state version names "v1"
    And 2 hours pass for the state versions of the stack "infra"
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "first"
    And token "h" eventually reads the run "first" in the state "READY" through A
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "first", its run token kept as "ft"
    And the worker takes over the run "first" with token "ft" and the key of "csr", checking out "infra", through A
    And the worker reports the run "first" started in INITIALIZING through B
    Then terraform "init" of the run "first" exits 0
    When the worker reports the run "first" succeeded in INITIALIZING through A
    Then the report's answer is to execute in PLANNING
    And terraform "plan" of the run "first" exits 0
    When the worker reports the run "first" succeeded in PLANNING with the delta '{"add":1,"change":0,"delete":0,"import":0,"outputs":false}' through B
    Then the report's answer is to execute in APPLYING
    And terraform "apply -auto-approve" of the run "first" exits 0
    And terraform "apply -auto-approve -replace=terraform_data.kept" of the run "first" exits 0
    And the run "first" wrote 2 state versions
    When token "h" searches the state versions of the stack "infra" with '{}' through B
    Then the answer's state versions are "first, first, imported, imported"
    And the answer's 1st state version names "r2"
    And the answer's 2nd state version names "r1"
    When the "state.retention" periodic task falls due
    Then "state.retention" eventually runs once more, as one job
    And the state version "v1" is deleted, with its object
    And the state versions "v2, r1, r2" are kept, with their objects
    When 2 hours pass for the state versions of the stack "infra"
    And the "state.retention" periodic task falls due
    Then "state.retention" eventually runs once more, as one job
    And the state version "v2" is deleted, with its object
    And the state versions "r1, r2" are kept, with their objects
    When token "h" searches the state versions of the stack "infra" with '{}' through A
    Then the answer's state versions are "first, first"
    And terraform "output -json" of the run "first" exits 0
    And the worker records the last output of the run "first" as its outputs.json through B
    When the worker reports the run "first" succeeded in APPLYING through A
    Then the report's answer is to exit in FINISHED
    When the "state.retention" periodic task falls due
    Then "state.retention" eventually runs once more, as one job
    And the state version "r1" is deleted, with its object
    And the state version "r2" is kept, with its object
    When token "h" searches the state versions of the stack "infra" with '{}' through B
    Then the answer's state versions are "first"
    When token "h" sends "POST /api/v1/stacks/infra/lock" through A
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/managed-state-rollback" with '{"managedStateVersionId":"{v1}"}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks/infra/managed-state-rollback" with '{"managedStateVersionId":"{r1}"}' through A
    Then it answers a 404 problem "not-found"
    And the audit trail holds 0 rollbacks of the stack "infra" by the home key
