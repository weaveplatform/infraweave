@postgres @objectstore @vcs
Feature: A run previews an uploaded local workspace
  A developer previews code they have not committed. They declare a
  tar.gz of their workspace by its size and SHA-256 and are answered an
  upload ID and a signed URL that accepts those bytes alone, put the
  archive there, and propose a run on the upload. The run is PROPOSED,
  with the stack's tracked commit as its commit, and a second proposal on
  the same upload answers it while it has not ended; a proposal with
  environment variable overrides creates a run each time, since no other
  run shares them, and no answer names them. Its specification
  names the archive's signed GET as its source, with no credential, and
  sets TF_VAR_infraweave_local_preview to true. Both calls need a stack
  that allows local previews, and a space writer; a space reader is
  refused each one's action. A size over 2 GiB, or a SHA-256 that is not
  64 lowercase hex digits, is an invalid request; an upload that is not
  the stack's, was never put, or does not exist is refused at /workspace,
  and an override of a variable infraweave sets at its key. The release indexes
  serve the recorded fixtures, so the run's specification names a
  Terraform release. The home key is space-admin on the account's top
  space; the member key holds only the bindings a scenario gives it.
  Stacks infra, other and plain live in legacy and run on the pool; infra
  and other allow local previews and plain does not.
  ref: decision 0007
  ref: decision 0008
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And the member key is exchanged through A for token "m"
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","localPreviewEnabled":true},"manageState":false}' through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Other","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","localPreviewEnabled":true},"manageState":false}' through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Plain","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}"},"manageState":false}' through B
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And a local workspace "ws" holding the files:
      | main.tf      | terraform {}                     |
      | variables.tf | variable "infraweave_local_preview" {} |

  @op:uploadLocalWorkspace @op:runProposeLocalWorkspace @case:success
  Scenario: A proposal on an uploaded workspace previews it on the tracked commit, and its worker fetches the archive
    Given the launcher "l" registers with the key of "csr" and the certificate "cert" through A
    When token "h" sends "POST /api/v1/stacks/infra/local-workspace-uploads" with '{"size":{ws.size},"sha256":"{ws.sha256}"}' through A
    Then it answers 201
    And the answer's id names "upload"
    And the local workspace "ws" is put at the answer's uploadUrl
    When token "h" sends "POST /api/v1/stacks/infra/runs/propose-local-workspace" with '{"workspace":"{upload}"}' through B
    Then it answers 200
    And the answer's type is '"PROPOSED"'
    And the answer's state is '"QUEUED"'
    And the answer's commits is '["{seed}"]'
    And the answer's id names "run"
    When token "h" sends "POST /api/v1/stacks/infra/runs/propose-local-workspace" with '{"workspace":"{upload}"}' through A
    Then it answers 200
    And the answer's id is '"{run}"'
    When token "h" sends "POST /api/v1/stacks/infra/runs/propose-local-workspace" with '{"workspace":"{upload}","environmentVarsOverrides":[{"key":"EXTRA","value":"pl41n-extra"}]}' through B
    Then it answers 200
    And the answer's id names "over"
    And neither the answer nor a server's log names "pl41n-extra"
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED"}' through B
    Then it answers 200
    And the answer's id names "commit"
    And token "h" eventually reads the run "run" in the state "READY" through A
    And token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then the answer assigns the run "run", its run token kept as "rt"
    When token "rt" sends "GET /worker/v1/runs/{run}/spec" through A
    Then it answers 200
    And the specification fetches its source from the local workspace "ws"
    And the specification's variable "TF_VAR_infraweave_local_preview" is "true"
    And the specification's sealed part opens with the key of "csr" to no source credential
    When token "h" sends "GET /api/v1/stacks/infra/proposed-runs" through B
    Then the answer's items are the runs "commit, over, run"

  @op:uploadLocalWorkspace @op:runProposeLocalWorkspace @case:denial
  Scenario: A space reader is refused both calls, and a stack that does not allow local previews refuses everyone
    Given the member key is space-reader on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/local-workspace-uploads" with '{"size":{ws.size},"sha256":"{ws.sha256}"}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_UPLOAD_LOCAL_WORKSPACE"'
    When token "h" sends "POST /api/v1/stacks/infra/local-workspace-uploads" with '{"size":{ws.size},"sha256":"{ws.sha256}"}' through B
    Then it answers 201
    And the answer's id names "upload"
    When token "m" sends "POST /api/v1/stacks/infra/runs/propose-local-workspace" with '{"workspace":"{upload}"}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_PROPOSE_LOCAL_WORKSPACE"'
    When token "h" sends "POST /api/v1/stacks/plain/local-workspace-uploads" with '{"size":{ws.size},"sha256":"{ws.sha256}"}' through B
    Then it answers a 403 problem "forbidden"
    When token "h" sends "POST /api/v1/stacks/plain/runs/propose-local-workspace" with '{"workspace":"{upload}"}' through A
    Then it answers a 403 problem "forbidden"
    Given the member key is space-writer on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/local-workspace-uploads" with '{"size":{ws.size},"sha256":"{ws.sha256}"}' through B
    Then it answers 201

  @op:uploadLocalWorkspace @op:runProposeLocalWorkspace @case:failure
  Scenario: A declaration outside the bounds, an upload that is not the stack's, was never put or does not exist, and an override of a variable infraweave sets are refused
    When token "h" sends "POST /api/v1/stacks/infra/local-workspace-uploads" with '{"size":0,"sha256":"{ws.sha256}"}' through A
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "POST /api/v1/stacks/infra/local-workspace-uploads" with '{"size":2147483649,"sha256":"{ws.sha256}"}' through B
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "POST /api/v1/stacks/infra/local-workspace-uploads" with '{"size":{ws.size},"sha256":"ABC"}' through A
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "POST /api/v1/stacks/other/local-workspace-uploads" with '{"size":{ws.size},"sha256":"{ws.sha256}"}' through B
    Then it answers 201
    And the answer's id names "foreign"
    And the local workspace "ws" is put at the answer's uploadUrl
    When token "h" sends "POST /api/v1/stacks/infra/runs/propose-local-workspace" with '{"workspace":"{foreign}"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/workspace" as "not-found"
    When token "h" sends "POST /api/v1/stacks/infra/local-workspace-uploads" with '{"size":{ws.size},"sha256":"{ws.sha256}"}' through B
    Then it answers 201
    And the answer's id names "unput"
    When token "h" sends "POST /api/v1/stacks/infra/runs/propose-local-workspace" with '{"workspace":"{unput}"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/workspace" as "not-found"
    When token "h" sends "POST /api/v1/stacks/infra/runs/propose-local-workspace" with '{"workspace":"{unput}","environmentVarsOverrides":[{"key":"INFRAWEAVE_API_TOKEN","value":"forged"}]}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/environmentVarsOverrides/0/key" as "rule"
    When token "h" sends "POST /api/v1/stacks/infra/runs/propose-local-workspace" with '{"workspace":"01HZX4Y9Q2W8E5R7T3V6K1M0PA"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/workspace" as "not-found"
    When token "h" sends "POST /api/v1/stacks/infra/runs/propose-local-workspace" with '{}' through A
    Then it answers a 400 problem "invalid-request"
