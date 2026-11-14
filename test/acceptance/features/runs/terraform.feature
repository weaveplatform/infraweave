@postgres @objectstore @vcs @docker @terraform
Feature: Runs of a Terraform stack end to end
  A real launcher of a private pool runs each run in a container of
  infraweave-runner-terraform:acceptance, with the newest Terraform the
  version catalogue lists, which it downloads from the suite's tool
  mirror and verifies against HashiCorp's signing key. A tracked run on a
  stack without autodeploy is triggered on a Gitea commit, initializes
  and plans, saves its workspace sealed to the pool's key, and waits in
  UNCONFIRMED; once confirmed, a new container restores the workspace,
  initializes again and applies, and the run finishes. Its hooks run in
  one shell session per container, so a variable a hook exports before
  initialization is set again after the restore. A write-only variable
  and a write-only mounted file reach the run, and the log masks each
  where a hook prints it; no object of the store holds either in the
  clear. A discarded run releases its stack's lock to the next run. A
  local preview plans an uploaded workspace that holds uncommitted code.
  In a run's environment a per-run override wins over the stack's
  variable, which wins over an attached context's; a stack variable that
  names a computed TF_VAR_infraweave_ variable replaces it; a ${{ … }}
  template renders against the winning values and the stack's computed
  fields; and a name keeps an ro_ prefix. Every run gets
  INFRAWEAVE_API_TOKEN, on a stack with no role too, whose subject is the
  stack's slug and the run's ID, and with which a hook reads its own stack
  and space. The release indexes serve the recorded fixtures; the home key
  is space-admin on the account's top space. Stacks live in legacy, keep
  their state in infraweave and run on the pool; the repository's
  configuration has terraform_data resources alone, so no provider is
  downloaded.
  ref: decision 0006
  ref: decision 0008
  ref: decision 0010
  ref: decision 0013
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | variable "secret" {\n  type      = string\n  sensitive = true\n  default   = "none"\n}\n\nresource "terraform_data" "this" {\n  input = "one"\n}\n\nresource "terraform_data" "secret" {\n  input = var.secret\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  @op:runTrigger @op:runConfirm @op:runLogs @op:runChangesV3 @case:success
  Scenario: A tracked run plans, waits for confirmation with its workspace saved, and applies once confirmed
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}},"beforeInit":["export GREETING=hello"],"beforePlan":["echo \"secret: $TF_VAR_secret\"","echo \"file: $(cat secret.txt)\""],"afterPlan":["echo \"after plan: $GREETING\""],"beforeApply":["echo \"before apply: $GREETING\""],"afterApply":["echo \"state pull: $(terraform state pull | jq -c \"{serial,lineage}\")\""]},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_secret","type":"ENVIRONMENT_VARIABLE","value":"s3cr3t-pl4n","writeOnly":true}' through A
    And token "h" sends "PUT /api/v1/stacks/infra/configs/secret.txt" with '{"type":"FILE_MOUNT","value":"ZjFsZS1zM2NyM3QK","writeOnly":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED"
    And token "h" reads the run "run"'s PLANNING log through B, which holds "secret: *****\nfile: *****" and not "s3cr3t"
    And token "h" reads the run "run"'s PLANNING log through A, which holds "infraweave: saved the workspace for the plan's confirmation" and not "f1le-s3cr3t"
    When token "h" sends "GET /api/v1/runs/{run}/changes-v3?input=%7B%7D" through A
    Then the answer's resources are the changes "terraform_data.secret ADD, terraform_data.this ADD"
    When token "h" sends "GET /api/v1/runs/{run}" through B
    Then the answer's delta is '{"addCount":2,"changeCount":0,"deleteCount":0,"importCount":0,"moveCount":0,"forgetCount":0,"resources":0,"okCount":0,"skipCount":0,"rescueCount":0,"ignoreCount":0,"unreachableCount":0,"failedCount":0}'
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through A
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, CONFIRMED, PREPARING_APPLY, APPLYING, FINISHED"
    And the outbox holds the run "run"'s run_updated events of the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, CONFIRMED, PREPARING_APPLY, APPLYING, FINISHED"
    And token "h" reads the run "run"'s APPLYING log through A, which holds "before apply: hello" and not "s3cr3t"
    And token "h" reads the run "run"'s APPLYING log through B, which holds "Apply complete! Resources: 2 added, 0 changed, 0 destroyed." and not "f1le-s3cr3t"
    And the run "run" wrote 1 state version
    And the current state version is the run "run"'s, with the serial and lineage its APPLYING log names after "state pull: ", as token "h" reads it through A
    And token "h" eventually reads a download URL of the run "run"'s PLANNING log through B
    And the answer's url downloads a log that holds "secret: *****" and not "s3cr3t"
    And no object in the scenario's bucket holds "s3cr3t-pl4n", of at least 6 objects
    And no object in the scenario's bucket holds "f1le-s3cr3t", of at least 6 objects
    And no answer of the scenario, log line of A or B, audit entry or event holds "s3cr3t-pl4n"

  @op:runDiscard @op:runTrigger @case:success
  Scenario: A run discarded from UNCONFIRMED ends, and its stack's lock passes to the next run
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "first"
    And token "h" eventually reads the run "first" in the state "UNCONFIRMED" through A
    And token "h" sends "POST /api/v1/stacks/infra/runs/discard" with '{"run":"{first}","note":"Not this one"}' through B
    Then it answers 200
    And the answer's state is '"DISCARDED"'
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "next"
    Then token "h" eventually reads the run "next" in the state "UNCONFIRMED" through A
    And token "h" eventually reads the run "first" in the state "DISCARDED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, DISCARDED"
    And the answer's last state transition's note is "Not this one"
    And the outbox holds the run "first"'s run_updated events of the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, DISCARDED"
    And the run "first" wrote 0 state versions

  @op:uploadLocalWorkspace @op:runProposeLocalWorkspace @case:success
  Scenario: A local preview plans the uncommitted code of an uploaded workspace, and finishes
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","localPreviewEnabled":true,"vendorConfig":{"terraform":{}},"beforePlan":["echo \"local preview: $TF_VAR_infraweave_local_preview\""]},"manageState":true}' through B
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And a local workspace "ws" holding the files:
      | main.tf | resource "terraform_data" "local" {\n  input = "uncommitted"\n}\n |
    When token "h" sends "POST /api/v1/stacks/infra/local-workspace-uploads" with '{"size":{ws.size},"sha256":"{ws.sha256}"}' through A
    Then it answers 201
    And the answer's id names "upload"
    And the local workspace "ws" is put at the answer's uploadUrl
    When token "h" sends "POST /api/v1/stacks/infra/runs/propose-local-workspace" with '{"workspace":"{upload}"}' through B
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FINISHED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FINISHED"
    And token "h" reads the run "run"'s PLANNING log through B, which holds "local preview: true" and not "terraform_data.this"
    When token "h" sends "GET /api/v1/runs/{run}/changes-v3?input=%7B%7D" through A
    Then the answer's resources are the changes "terraform_data.local ADD"
    And the run "run" wrote 0 state versions

  @op:proposeWithOverrides @case:success
  Scenario: A proposed run's environment ranks its sources, and its in-run API token reads its own stack and space
    Given token "h" sends "POST /api/v1/contexts" with '{"name":"Shared","space":"legacy","configAttachments":[{"id":"LEVEL","type":"ENVIRONMENT_VARIABLE","value":"context","writeOnly":false},{"id":"CTX_ONLY","type":"ENVIRONMENT_VARIABLE","value":"context","writeOnly":false}]}' through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}},"beforePlan":["env | grep -E \"^(CTX_ONLY|ECHO|LEVEL|ro_PLAIN|STACK_ONLY|TF_VAR_infraweave_run_type|TF_VAR_infraweave_stack_id)=\" | sort","echo \"api token: ${INFRAWEAVE_API_TOKEN:+set}\"","p=$(printf %s \"$INFRAWEAVE_API_TOKEN\" | cut -d. -f2 | tr _- /+)","while [ $(( ${#p} % 4 )) -ne 0 ]; do p=\"$p=\"; done","sub=$(printf %s \"$p\" | base64 -d | jq -r .sub)","echo \"token sub: $sub\"","s=$(curl -fsS -H \"Authorization: Bearer $INFRAWEAVE_API_TOKEN\" {relay.url}/api/v1/stacks/${sub%%/*})","echo \"stack read: $(echo \"$s\" | jq -r .id) in $(echo \"$s\" | jq -r .space)\"","sp=$(curl -fsS -H \"Authorization: Bearer $INFRAWEAVE_API_TOKEN\" {relay.url}/api/v1/spaces/$(echo \"$s\" | jq -r .space))","echo \"space read: $(echo \"$sp\" | jq -r .id)\""]},"manageState":true}' through B
    And token "h" sends "PUT /api/v1/stacks/infra/contexts/shared" with '{"priority":1}' through A
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"LEVEL","type":"ENVIRONMENT_VARIABLE","value":"stack","writeOnly":false}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"STACK_ONLY","type":"ENVIRONMENT_VARIABLE","value":"stack","writeOnly":false}' through A
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"ECHO","type":"ENVIRONMENT_VARIABLE","value":"${{ env.LEVEL }} of ${{ stack.id }}","writeOnly":false}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"ro_PLAIN","type":"ENVIRONMENT_VARIABLE","value":"plain","writeOnly":false}' through A
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_infraweave_run_type","type":"ENVIRONMENT_VARIABLE","value":"stack","writeOnly":false}' through B
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    When token "h" sends "POST /api/v1/stacks/infra/proposed-runs" with '{"environmentVarsOverrides":[{"key":"LEVEL","value":"run"}]}' through A
    Then it answers 201
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And token "h" reads the run "run"'s PLANNING log through A, which holds "CTX_ONLY=context\nECHO=run of infra\nLEVEL=run\nSTACK_ONLY=stack\nTF_VAR_infraweave_run_type=stack\nTF_VAR_infraweave_stack_id=infra\nro_PLAIN=plain\napi token: set" and not "LEVEL=stack"
    And token "h" reads the run "run"'s PLANNING log through B, which holds "token sub: infra/{run}\nstack read: infra in legacy\nspace read: legacy" and not "LEVEL=context"
