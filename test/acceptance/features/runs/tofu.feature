@postgres @objectstore @vcs @docker @tofu
Feature: Runs of an OpenTofu stack end to end
  A real launcher of a private pool runs each run in a container of
  {runner.opentofu}, with the newest OpenTofu the
  version catalogue lists, which it downloads from the suite's tool mirror
  and verifies against OpenTofu's signing key. A tracked run on a stack
  without autodeploy is triggered on a Gitea commit, initializes and
  plans, saves its workspace sealed to the pool's key, and waits in
  UNCONFIRMED; once confirmed, a new container restores the workspace,
  initializes again and applies, and the run finishes, its outputs kept as
  the stack's, each with its description from the plan and a sensitive one
  with no value. The launcher does not keep sensitive outputs' values, so
  a sensitive output is kept with no value where the stack enables
  sensitive output upload too. Its hooks run in one shell session per container, so a
  variable a hook exports before initialization is set again after the
  restore. A write-only variable and a write-only mounted file reach the
  run, and the log masks each where a hook prints it; no object of the
  store holds either in the clear. A discarded run releases its stack's
  lock to the next run. A local preview plans an uploaded workspace that
  holds uncommitted code. A task performs its command after
  initialization, reading the state's output; one that fails fails in
  PERFORMING, its output masked, and one that skips initialization runs
  no init command or hook and logs the skip. A stack deleted with its
  resources destroys them in a destroy run, whose after-destroy hook
  finds no resource left in the state, and is deleted when that run
  finishes. In a run's environment a per-run
  override wins over the stack's variable, which wins over an attached
  context's; a stack variable that names a computed TF_VAR_infraweave_
  variable replaces it; a ${{ … }} template renders against the winning
  values and the stack's computed fields; and a name keeps an ro_ prefix.
  Every run gets INFRAWEAVE_API_TOKEN, on a stack with no role too, whose
  subject is the stack's slug and the run's ID, and with which a hook
  reads its own stack and space. A run of a stack that enables CPU
  profiling uploads its planning's profile, whose samples each name the
  workflow command they come from; a stack that does not has none. The
  release indexes serve the recorded fixtures. A failed proposed run
  retries with its recorded hooks even after the stack changes them. The
  home key is space-admin on the account's top space.
  Stacks live in legacy, keep their state in infraweave and run on the
  pool; the repository's configuration has terraform_data resources and
  outputs alone, so no provider is downloaded.
  ref: decision 0006
  ref: decision 0008
  ref: decision 0010
  ref: decision 0013
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | variable "secret" {\n  type      = string\n  sensitive = true\n  default   = "none"\n}\n\nresource "terraform_data" "this" {\n  input = "one"\n}\n\nresource "terraform_data" "secret" {\n  input = var.secret\n}\n\noutput "name" {\n  description = "The input of terraform_data.this"\n  value       = terraform_data.this.output\n}\n\noutput "secret" {\n  value     = var.secret\n  sensitive = true\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the launchers started from now on set the run request limits to 17 per second and a burst of 170
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  @op:runTrigger @op:runRetry @case:success
  Scenario: A failed OpenTofu run retries its recorded hooks after the stack changes
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}},"beforePlan":["echo hook: old","exit 1"]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's runtime configuration includes '{"beforePlan":["echo hook: old","exit 1"],"terraformVersion":"{opentofu.newest}","terraformWorkflowTool":"OPEN_TOFU"}'
    Given token "h" sends "PATCH /api/v1/stacks/infra" with '{"beforePlan":["echo hook: new"]}' through A
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/retry" through B
    Then it answers 200
    And the answer's type is '"PROPOSED"'
    And the answer's commits is '["{seed}"]'
    And the answer's last state transition's note is "retry of {run}"
    And the answer's runtime configuration includes '{"beforePlan":["echo hook: old","exit 1"],"terraformVersion":"{opentofu.newest}","terraformWorkflowTool":"OPEN_TOFU"}'
    And the answer's id names "retry"
    And token "h" eventually reads the run "retry" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FAILED"
    And token "h" reads the run "retry"'s PLANNING log through B, which holds "hook: old" and not "hook: new"
    And token "h" records the run "retry"'s retry planning evidence through A

  @op:runTrigger @op:runConfirm @op:runLogs @op:runChangesV3 @case:success
  Scenario: A tracked run plans, waits for confirmation with its workspace saved, and applies once confirmed
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}},"beforeInit":["export GREETING=hello"],"beforePlan":["echo \"secret: $TF_VAR_secret\"","echo \"file: $(cat /mnt/workspace/secret.txt)\"","pwd"],"afterPlan":["echo \"after plan: $GREETING\""],"beforeApply":["echo \"before apply: $GREETING\""],"afterApply":["echo \"state pull: $(tofu state pull | jq -c \"{serial,lineage}\")\""]},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_secret","type":"ENVIRONMENT_VARIABLE","value":"s3cr3t-pl4n","writeOnly":true}' through A
    And token "h" sends "PUT /api/v1/stacks/infra/configs/secret.txt" with '{"type":"FILE_MOUNT","value":"ZjFsZS1zM2NyM3QK","writeOnly":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED"
    And token "h" reads the run "run"'s PLANNING log through B, which holds "secret: *****\nfile: *****" and not "s3cr3t"
    And token "h" reads the run "run"'s PLANNING log through A, which holds "/mnt/workspace/source" and not "Error"
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
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then the answer's outputs is '[{"id":"name","description":"The input of terraform_data.this","sensitive":false,"value":"\"one\""},{"id":"secret","sensitive":true}]'
    And token "h" eventually reads a download URL of the run "run"'s PLANNING log through B
    And the answer's url downloads a log that holds "secret: *****" and not "s3cr3t"
    And no object in the scenario's bucket holds "s3cr3t-pl4n", of at least 6 objects
    And no object in the scenario's bucket holds "f1le-s3cr3t", of at least 6 objects
    And no answer of the scenario, log line of A or B, audit entry or event holds "s3cr3t-pl4n"

  @op:run @op:runConfirm @case:success
  Scenario: A confirmation keeps its recorded hooks and release while rereading variables
    Given the fixture's previous OpenTofu release is kept as "older"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","projectRoot":".","vendorConfig":{"opentofu":{}},"beforePlan":["echo hook: planned"],"beforeApply":["echo hook: recorded apply","tofu version","echo \"value: $HANDOVER_VALUE\""]},"manageState":true}' through A
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"HANDOVER_VALUE","type":"ENVIRONMENT_VARIABLE","value":"before","writeOnly":false}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    Then the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "UNCONFIRMED" through B
    And the answer's runtime configuration includes '{"beforePlan":["echo hook: planned"],"beforeApply":["echo hook: recorded apply","tofu version","echo \"value: $HANDOVER_VALUE\""],"terraformVersion":"{opentofu.newest}","terraformWorkflowTool":"OPEN_TOFU","projectRoot":".","runnerImage":"","environment":[],"gitSparseCheckoutPaths":[]}'
    Given the member key is exchanged through A for token "m"
    And the member key is space-reader on the space "legacy"
    When token "m" sends "GET /api/v1/runs/{run}" through A
    Then it answers 200
    And the answer's runtime configuration includes '{"beforePlan":["echo hook: planned"],"beforeApply":["echo hook: recorded apply","tofu version","echo \"value: $HANDOVER_VALUE\""],"terraformVersion":"{opentofu.newest}","terraformWorkflowTool":"OPEN_TOFU","projectRoot":".","runnerImage":"","environment":[],"gitSparseCheckoutPaths":[]}'
    When token "h" sends "PATCH /api/v1/stacks/infra" with '{"beforeApply":["echo hook: edited apply","exit 9"],"terraformVersion":"{older}"}' through B
    Then it answers 200
    And token "h" sends "PUT /api/v1/stacks/infra/configs/HANDOVER_VALUE" with '{"type":"ENVIRONMENT_VARIABLE","value":"after","writeOnly":false}' through A
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through B
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FINISHED" through A
    And the answer's runtime configuration includes '{"beforePlan":["echo hook: planned"],"beforeApply":["echo hook: recorded apply","tofu version","echo \"value: $HANDOVER_VALUE\""],"terraformVersion":"{opentofu.newest}","terraformWorkflowTool":"OPEN_TOFU","projectRoot":".","runnerImage":"","environment":[],"gitSparseCheckoutPaths":[]}'
    And token "h" reads the run "run"'s APPLYING log through A, which holds "hook: recorded apply" and not "hook: edited apply"
    And token "h" reads the run "run"'s APPLYING log through B, which holds "OpenTofu v{opentofu.newest}" and not "OpenTofu v{older}"
    And token "h" records the run "run"'s APPLYING release evidence through B
    And token "h" reads the run "run"'s APPLYING log through A, which holds "value: after" and not "value: before"

  @op:run @op:runTrigger @case:failure
  Scenario: A first handover that resolves no release leaves no runtime configuration
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Unresolvable","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}},"terraformVersion":"> 9999.0.0"},"manageState":true}' through A
    When token "h" sends "POST /api/v1/stacks/unresolvable/runs" with '{"commitSha":"{seed}"}' through B
    Then the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FAILED" through A
    And the answer leaves out "runtimeConfig"

  @op:runTrigger @op:runConfirm @op:contextCreateV2 @op:contextAttach @op:stackConfigSet @case:success
  Scenario: Readable and secret mounted files preserve text and binary bytes in plan and apply
    Given token "h" sends "POST /api/v1/contexts" with '{"name":"Files","space":"legacy","configAttachments":[{"id":"readable.txt","type":"FILE_MOUNT","value":"enVsdQo=","writeOnly":false},{"id":"readable.bin","type":"FILE_MOUNT","value":"AP9BCgA=","writeOnly":false}]}' through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}},"beforePlan":["printf \"zulu\\n\" | cmp - /mnt/workspace/readable.txt","printf \"\\146\\061\\154\\145\\055\\163\\063\\143\\162\\063\\164\\012\" | cmp - /mnt/workspace/secret.txt","printf \"\\000\\377A\\012\\000\" | cmp - /mnt/workspace/readable.bin","printf \"\\000\\377A\\012\\000\" | cmp - /mnt/workspace/secret.bin","echo mounted bytes match"],"beforeApply":["printf \"zulu\\n\" | cmp - /mnt/workspace/readable.txt","printf \"\\146\\061\\154\\145\\055\\163\\063\\143\\162\\063\\164\\012\" | cmp - /mnt/workspace/secret.txt","printf \"\\000\\377A\\012\\000\" | cmp - /mnt/workspace/readable.bin","printf \"\\000\\377A\\012\\000\" | cmp - /mnt/workspace/secret.bin","echo mounted bytes match"]},"manageState":true}' through B
    And token "h" sends "PUT /api/v1/stacks/infra/contexts/files" with '{"priority":0}' through A
    And token "h" sends "PUT /api/v1/stacks/infra/configs/secret.txt" with '{"type":"FILE_MOUNT","value":"ZjFsZS1zM2NyM3QK","writeOnly":true}' through B
    And token "h" sends "PUT /api/v1/stacks/infra/configs/secret.bin" with '{"type":"FILE_MOUNT","value":"AP9BCgA=","writeOnly":true,"fileMode":"600"}' through A
    Then it answers 201
    And the answer's checksum is '"e67b57605dd765b72c935374eef290a3c3f37796778003c13405cd4300b7ac97"'
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    Then the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And token "h" reads the run "run"'s PLANNING log through B, which holds "mounted bytes match" and not "f1le-s3cr3t"
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through A
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And token "h" reads the run "run"'s APPLYING log through A, which holds "mounted bytes match" and not "f1le-s3cr3t"
    When token "h" sends "GET /api/v1/contexts/files/config-element?id=readable.bin" through B
    Then it answers 200
    And the answer's value is '"AP9BCgA="'
    And the answer's checksum is '"e67b57605dd765b72c935374eef290a3c3f37796778003c13405cd4300b7ac97"'
    When token "h" sends "GET /api/v1/stacks/infra/config-element?id=secret.bin" through A
    Then it answers 200
    And the answer's checksum is '"e67b57605dd765b72c935374eef290a3c3f37796778003c13405cd4300b7ac97"'
    And the answer leaves out "value"
    And no object in the scenario's bucket holds "f1le-s3cr3t", of at least 6 objects
    And no answer of the scenario, log line of A or B, audit entry or event holds "f1le-s3cr3t"

  @op:runDiscard @op:runTrigger @op:runCpuProfileDownloadURL @case:success
  Scenario: A run discarded from UNCONFIRMED ends, and its stack's lock passes to the next run
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}}},"manageState":true}' through B
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
    When token "h" sends "GET /api/v1/runs/{first}/cpu-profile-download-url?state=PLANNING" through A
    Then it answers 200
    And the answer's expired is 'true'
    And the answer leaves out "url"

  @op:runCpuProfileDownloadURL @op:runTrigger @case:success
  Scenario: A run of a stack that enables CPU profiling uploads its planning's profile, each sample naming its command
    Given a Gitea repository "heavy" with the files:
      | main.tf | locals {\n  cells = flatten([for i in range(400) : [for j in range(400) : i * j]])\n}\n\noutput "cells" {\n  value = length(local.cells)\n}\n |
    And the head of "main" of "heavy" is kept as "heavyseed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Heavy","repository":"heavy","branch":"main","provider":"GIT","repositoryURL":"{heavy.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{"enableCPUProfiling":true}}},"manageState":true}' through B
    And token "h" eventually reads the stack "heavy" tracking the commit kept as "heavyseed" through A
    When token "h" sends "POST /api/v1/stacks/heavy/runs" with '{"runType":"PROPOSED","commitSha":"{heavyseed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FINISHED"
    When token "h" sends "GET /api/v1/runs/{run}/cpu-profile-download-url?state=PLANNING" through A
    Then it answers 200
    And the answer's url downloads a CPU profile whose samples each name one of the commands "plan, showPlan"

  @op:uploadLocalWorkspace @op:runProposeLocalWorkspace @case:success
  Scenario: A local preview plans the uncommitted code of an uploaded workspace, and finishes
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","localPreviewEnabled":true,"vendorConfig":{"opentofu":{}},"beforePlan":["echo \"local preview: $TF_VAR_infraweave_local_preview\""]},"manageState":true}' through B
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

  @op:proposeWithOverrides @case:success @op:runTrigger @case:denial
  Scenario: A proposed run's environment ranks its sources, and its in-run API token reads its own stack and space
    Given token "h" sends "POST /api/v1/contexts" with '{"name":"Shared","space":"legacy","configAttachments":[{"id":"LEVEL","type":"ENVIRONMENT_VARIABLE","value":"context","writeOnly":false},{"id":"CTX_ONLY","type":"ENVIRONMENT_VARIABLE","value":"context","writeOnly":false}]}' through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}},"beforePlan":["env | grep -E \"^(CTX_ONLY|ECHO|LEVEL|ro_PLAIN|STACK_ONLY|TF_VAR_infraweave_run_type|TF_VAR_infraweave_stack_id|TF_VAR_infraweave_api_endpoint|INFRAWEAVE_MAX_REQUESTS_PER_SECOND|INFRAWEAVE_MAX_REQUESTS_BURST|INFRAWEAVE_SENSITIVE_OUTPUT_UPLOAD_ENABLED|INFRAWEAVE_VENDOR_BINARY_CACHED|TF_APPEND_USER_AGENT)=\" | sort","echo \"api token: ${INFRAWEAVE_API_TOKEN:+set}\"","p=$(printf %s \"$INFRAWEAVE_API_TOKEN\" | cut -d. -f2 | tr _- /+)","while [ $(( ${#p} % 4 )) -ne 0 ]; do p=\"$p=\"; done","sub=$(printf %s \"$p\" | base64 -d | jq -r .sub)","echo \"token sub: $sub\"","s=$(curl -fsS -H \"Authorization: Bearer $INFRAWEAVE_API_TOKEN\" $TF_VAR_infraweave_api_endpoint/stacks/${sub%%/*})","echo \"stack read: $(echo \"$s\" | jq -r .id) in $(echo \"$s\" | jq -r .space)\"","sp=$(curl -fsS -H \"Authorization: Bearer $INFRAWEAVE_API_TOKEN\" $TF_VAR_infraweave_api_endpoint/spaces/$(echo \"$s\" | jq -r .space))","echo \"space read: $(echo \"$sp\" | jq -r .id)\"","code=$(curl -sS -o /tmp/infraweave-denied.json -w \"%{http_code}\" -X POST -H \"Authorization: Bearer $INFRAWEAVE_API_TOKEN\" -H \"Content-Type: application/json\" --data \"{}\" \"$TF_VAR_infraweave_api_endpoint/stacks/${sub%%/*}/runs\")","echo \"run trigger: $code $(jq -r .type /tmp/infraweave-denied.json)\"","pwd"]},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"LEVEL","type":"ENVIRONMENT_VARIABLE","value":"stack","writeOnly":false}' through B
    And token "h" sends "PUT /api/v1/stacks/infra/contexts/shared" with '{"priority":1}' through A
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"STACK_ONLY","type":"ENVIRONMENT_VARIABLE","value":"stack","writeOnly":false}' through A
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"ECHO","type":"ENVIRONMENT_VARIABLE","value":"${{ env.LEVEL }} of ${{ stack.id }}","writeOnly":false}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"ro_PLAIN","type":"ENVIRONMENT_VARIABLE","value":"plain","writeOnly":false}' through A
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_infraweave_run_type","type":"ENVIRONMENT_VARIABLE","value":"stack","writeOnly":false}' through B
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"source/main.tf","type":"FILE_MOUNT","value":"dmFyaWFibGUgInNlY3JldCIgewogIHR5cGUgICAgICA9IHN0cmluZwogIHNlbnNpdGl2ZSA9IHRydWUKICBkZWZhdWx0ICAgPSAibm9uZSIKfQoKcmVzb3VyY2UgInRlcnJhZm9ybV9kYXRhIiAidGhpcyIgewogIGlucHV0ID0gIm9uZSIKfQoKcmVzb3VyY2UgInRlcnJhZm9ybV9kYXRhIiAic2VjcmV0IiB7CiAgaW5wdXQgPSB2YXIuc2VjcmV0Cn0KCm91dHB1dCAibmFtZSIgewogIGRlc2NyaXB0aW9uID0gIlRoZSBpbnB1dCBvZiB0ZXJyYWZvcm1fZGF0YS50aGlzIgogIHZhbHVlICAgICAgID0gdGVycmFmb3JtX2RhdGEudGhpcy5vdXRwdXQKfQoKb3V0cHV0ICJzZWNyZXQiIHsKICB2YWx1ZSAgICAgPSB2YXIuc2VjcmV0CiAgc2Vuc2l0aXZlID0gdHJ1ZQp9CgpvdXRwdXQgIndoZXJlIiB7CiAgdmFsdWUgPSAibW91bnRlZCIKfQo=","writeOnly":false}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/stacks/infra/proposed-runs" with '{"environmentVarsOverrides":[{"key":"LEVEL","value":"run"}]}' through A
    Then it answers 201
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And token "h" reads the run "run"'s PLANNING log through A, which holds "CTX_ONLY=context\nECHO=run of infra\nINFRAWEAVE_MAX_REQUESTS_BURST=170\nINFRAWEAVE_MAX_REQUESTS_PER_SECOND=17\nINFRAWEAVE_SENSITIVE_OUTPUT_UPLOAD_ENABLED=false\nINFRAWEAVE_VENDOR_BINARY_CACHED=true\nLEVEL=run\nSTACK_ONLY=stack" and not "LEVEL=stack"
    And token "h" reads the run "run"'s PLANNING log through A, which holds "TF_APPEND_USER_AGENT=infraweave/" and not "TF_APPEND_USER_AGENT=container"
    And token "h" reads the run "run"'s PLANNING log through A, which holds "TF_VAR_infraweave_api_endpoint={relay.url}/api/v1\nTF_VAR_infraweave_run_type=stack\nTF_VAR_infraweave_stack_id=infra\nro_PLAIN=plain\napi token: set" and not "LEVEL=stack"
    And token "h" reads the run "run"'s PLANNING log through B, which holds "token sub: infra/{run}\nstack read: infra in legacy\nspace read: legacy\nrun trigger: 403 urn:infraweave:problem:forbidden" and not "LEVEL=context"
    And token "h" records the run "run"'s workspace layout evidence through B

  @op:runTrigger @op:contextCreateV2 @op:contextAttach @op:contextConfigAdd @case:success
  Scenario: Auto-attached contexts rank variables and hooks from Z to A
    Given token "h" sends "POST /api/v1/contexts" with '{"name":"Alpha","space":"legacy","configAttachments":[{"id":"LEVEL","type":"ENVIRONMENT_VARIABLE","value":"alpha","writeOnly":false}],"hooks":{"afterApply":[],"afterDestroy":[],"afterInit":[],"afterPerform":[],"afterPlan":["echo after-alpha"],"afterRun":[],"beforeApply":[],"beforeDestroy":[],"beforeInit":[],"beforePerform":[],"beforePlan":["echo alpha"]},"labels":["autoattach:infra"]}' through A
    Then it answers 201
    And token "h" sends "POST /api/v1/contexts" with '{"name":"Zulu","space":"legacy","configAttachments":[{"id":"LEVEL","type":"ENVIRONMENT_VARIABLE","value":"zulu","writeOnly":false}],"hooks":{"afterApply":[],"afterDestroy":[],"afterInit":[],"afterPerform":[],"afterPlan":["echo after-zulu"],"afterRun":[],"beforeApply":[],"beforeDestroy":[],"beforeInit":[],"beforePerform":[],"beforePlan":["echo zulu"]},"labels":["autoattach:infra"]}' through A
    Then it answers 201
    And token "h" sends "POST /api/v1/contexts" with '{"name":"Direct","space":"legacy","configAttachments":[{"id":"DIRECT_ONLY","type":"ENVIRONMENT_VARIABLE","value":"direct","writeOnly":false}],"hooks":{"afterApply":[],"afterDestroy":[],"afterInit":[],"afterPerform":[],"afterPlan":["echo after-direct"],"afterRun":[],"beforeApply":[],"beforeDestroy":[],"beforeInit":[],"beforePerform":[],"beforePlan":["echo \"DIRECT_ONLY=$DIRECT_ONLY\"","echo \"LEVEL=$LEVEL\"","echo direct"]}}' through A
    Then it answers 201
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","labels":["infra"],"vendorConfig":{"opentofu":{}},"beforePlan":["echo stack"],"afterPlan":["echo after-stack"]},"manageState":true}' through B
    Then it answers 201
    And token "h" sends "PUT /api/v1/stacks/infra/contexts/direct" with '{"priority":5}' through A
    Then it answers 200
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through B
    Then it answers 200
    And the answer's id names "auto"
    And token "h" eventually reads the run "auto" in the state "FINISHED" through A
    And token "h" reads the run "auto"'s PLANNING log through B, which holds "DIRECT_ONLY=direct\nLEVEL=zulu\ndirect\nzulu\nalpha\nstack" and not "LEVEL=alpha"
    And token "h" reads the run "auto"'s PLANNING log through A, which holds "after-stack\nafter-alpha\nafter-zulu\nafter-direct" and not "LEVEL=alpha"
    When token "h" sends "POST /api/v1/contexts/direct/configs" with '{"id":"LEVEL","type":"ENVIRONMENT_VARIABLE","value":"direct","writeOnly":false}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    Then it answers 200
    And the answer's id names "direct"
    And token "h" eventually reads the run "direct" in the state "FINISHED" through B
    And token "h" reads the run "direct"'s PLANNING log through A, which holds "DIRECT_ONLY=direct\nLEVEL=direct\ndirect\nzulu\nalpha\nstack" and not "LEVEL=zulu"

  @op:taskCreate @op:runLogs @case:success
  Scenario: A task performs its command on the stack's state, and a failing task fails in PERFORMING with its output masked
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"opentofu":{}},"beforeInit":["echo hook: before init"],"afterInit":["echo hook: after init"],"beforePerform":["echo \"before perform: $TF_VAR_infraweave_run_type\""],"afterPerform":["echo \"after perform\""]},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_secret","type":"ENVIRONMENT_VARIABLE","value":"s3cr3t-t4sk","writeOnly":true}' through A
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FINISHED" through A
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"echo \"name: $(tofu output -raw name)\""}' through A
    Then it answers 201
    And the answer's id names "task"
    And token "h" eventually reads the run "task" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PERFORMING, FINISHED"
    And token "h" reads the run "task"'s PERFORMING log through A, which holds "before perform: TASK\nname: one\nafter perform" and not "s3cr3t"
    And the run "task" wrote 0 state versions
    And token "h" reads the run "task"'s INITIALIZING log through A, which holds "hook: before init" and not "Error"
    And token "h" reads the run "task"'s INITIALIZING log through B, which holds "hook: after init" and not "Error"
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"echo \"secret: $TF_VAR_secret\"; exit 3","skipInitialization":true}' through B
    Then it answers 201
    And the answer's id names "failed"
    And token "h" eventually reads the run "failed" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PERFORMING, FAILED"
    And the answer's last state transition's note is "The task's command failed with exit status 3."
    And token "h" reads the run "failed"'s INITIALIZING log through B, which holds "infraweave: initialization is skipped" and not "Initializing the backend"
    And token "h" reads the run "failed"'s INITIALIZING log through A, which holds "infraweave: initialization is skipped" and not "hook: before init"
    And token "h" reads the run "failed"'s INITIALIZING log through B, which holds "infraweave: initialization is skipped" and not "hook: after init"
    And token "h" reads the run "failed"'s PERFORMING log through A, which holds "secret: *****" and not "s3cr3t"

  @op:stackDelete @case:success
  Scenario: A stack deleted with its resources destroys them in a destroy run that leaves its state empty, and is deleted when the run finishes
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"enableSensitiveOutputUpload":true,"vendorConfig":{"opentofu":{}},"afterDestroy":["test -z \"$(tofu state list)\""]},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the run "run" wrote 1 state version
    And the run "run" kept its stack's output "name" with a sealed value
    And the run "run" kept its stack's output "secret" with no sealed value
    When token "h" sends "DELETE /api/v1/stacks/infra?destroyResources=true" through A
    Then it answers 200
    And the answer's deleting is 'true'
    When token "h" sends "GET /api/v1/stacks/infra/runs" through B
    Then the answer's first run names "destroy"
    And token "h" eventually finds no stack "infra" through A
    And the outbox holds the run "destroy"'s run_updated events of the states "QUEUED, READY, PREPARING, INITIALIZING, DESTROYING, FINISHED"
