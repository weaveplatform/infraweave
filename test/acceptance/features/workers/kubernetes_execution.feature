@kubernetes-lab @kubernetes @postgres @vcs
Feature: Runs in Kubernetes Pods
  A real worker controller hosts a Managed pool's slot in the owned
  cluster, and its launcher executes each run the slot claims in a Pod of
  the run's runner image. The Pod's bootstrap init container, of an image
  holding the current infraweave build that the suite imports into the
  cluster's node, receives the run's workspace, its verified tools and
  the infraweave executable through its attached standard input; the
  runner then receives the handoff, with the opened secrets, through its
  own. The Pods reach the servers, and the objects they sign, only
  through the scenario's HTTPS front, whose private authority the
  controller's ca_file names and the handoff carries to the runner and
  its tools. A tracked OpenTofu run plans in one Pod, saves its workspace
  sealed to the pool's key, waits in UNCONFIRMED, and once confirmed
  applies in another Pod that restores it; a write-only variable and a
  write-only mounted file reach the run and are masked in its log, and
  appear in no Pod, Worker resource, controller log, object or answer.
  Every Pod runs as UID 1983 without privilege, capabilities, a service
  account token or a host path, is owned by a Worker of its pool, and is
  gone once its run ends. A Terraform stack's autodeployed run applies
  through the normal state backend, and its local preview plans an
  uploaded workspace. A run whose runner image cannot be pulled, and one
  whose pool's Pods select nodes of another architecture than its tools,
  fail in PREPARING and leave no Pod. Repositories are Gitea's; the home
  key is space-admin on the account's top space.
  ref: decision 0008
  ref: decision 0013
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
      | main.tf | variable "secret" {\n  type      = string\n  sensitive = true\n  default   = "none"\n}\n\nresource "terraform_data" "this" {\n  input = "one"\n}\n\nresource "terraform_data" "secret" {\n  input = var.secret\n}\n\noutput "name" {\n  value = terraform_data.this.output\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And 1 worker controller watch the controller namespaces
    And the Managed WorkerPool "build" of capacity 1 in the space "team" is applied in "home"
    And the WorkerPool "build" eventually has 1 registered slots, its pool kept as "build"
    And the WorkerPool "build" eventually reports ExecutorAvailable True "LauncherRunning"

  @tofu @op:runTrigger @op:runConfirm @case:success
  Scenario: A tracked OpenTofu run plans in one Pod and applies in another that restores its sealed workspace
    Given token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}","workerPool":"{build}","vendorConfig":{"opentofu":{}},"beforeInit":["export GREETING=hello"],"beforePlan":["echo \"secret: $TF_VAR_secret\"","echo \"file: $(cat /mnt/workspace/secret.txt)\""],"beforeApply":["echo \"before apply: $GREETING\""]},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_secret","type":"ENVIRONMENT_VARIABLE","value":"s3cr3t-pl4n","writeOnly":true}' through A
    And token "h" sends "PUT /api/v1/stacks/infra/configs/secret.txt" with '{"type":"FILE_MOUNT","value":"ZjFsZS1zM2NyM3QK","writeOnly":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED"
    And token "h" reads the run "run"'s PLANNING log through B, which holds "secret: *****\nfile: *****" and not "s3cr3t"
    And token "h" reads the run "run"'s PLANNING log through A, which holds "infraweave: saved the workspace for the plan's confirmation" and not "f1le-s3cr3t"
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through A
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, CONFIRMED, PREPARING_APPLY, APPLYING, FINISHED"
    And token "h" reads the run "run"'s APPLYING log through A, which holds "before apply: hello" and not "s3cr3t"
    And token "h" reads the run "run"'s APPLYING log through B, which holds "Apply complete! Resources: 2 added, 0 changed, 0 destroyed." and not "f1le-s3cr3t"
    And the run "run" wrote 1 state version
    And the run "run" ran in Pods of the WorkerPool "build" from the image "{runner.opentofu}", each hardened and holding no "s3cr3t-pl4n"
    And the run "run" eventually has no Pod
    And no run Pod, Worker resource or worker controller log line holds "s3cr3t-pl4n"
    And no run Pod, Worker resource or worker controller log line holds "f1le-s3cr3t"
    And no object in the scenario's bucket holds "s3cr3t-pl4n", of at least 6 objects
    And no object in the scenario's bucket holds "f1le-s3cr3t", of at least 6 objects
    And no answer of the scenario, log line of A or B, audit entry or event holds "s3cr3t-pl4n"

  @terraform @op:runTrigger @op:run @case:success
  Scenario: A Terraform stack's autodeployed run applies through the state backend, and its local preview plans an uploaded workspace
    Given token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}","workerPool":"{build}","autodeploy":true,"localPreviewEnabled":true,"vendorConfig":{"terraform":{}},"beforePlan":["echo \"local preview: $TF_VAR_infraweave_local_preview\""]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, APPLYING, FINISHED"
    And token "h" reads the run "run"'s APPLYING log through A, which holds "Apply complete! Resources: 2 added, 0 changed, 0 destroyed." and not "Error"
    And the run "run" wrote 1 state version
    And the run "run" ran in Pods of the WorkerPool "build" from the image "{runner.terraform}", each hardened and holding no "TF_VAR_secret"
    Given a local workspace "ws" holding the files:
      | main.tf | resource "terraform_data" "local" {\n  input = "uncommitted"\n}\n |
    When token "h" sends "POST /api/v1/stacks/infra/local-workspace-uploads" with '{"size":{ws.size},"sha256":"{ws.sha256}"}' through A
    Then it answers 201
    And the answer's id names "upload"
    And the local workspace "ws" is put at the answer's uploadUrl
    When token "h" sends "POST /api/v1/stacks/infra/runs/propose-local-workspace" with '{"workspace":"{upload}"}' through B
    Then it answers 200
    And the answer's id names "preview"
    And token "h" eventually reads the run "preview" in the state "FINISHED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FINISHED"
    And token "h" reads the run "preview"'s PLANNING log through B, which holds "local preview: true" and not "Error"
    And token "h" reads the run "preview"'s PLANNING log through A, which holds "terraform_data.local will be created" and not "terraform_data.this will be created"
    And the run "preview" wrote 0 state versions
    And the run "preview" eventually has no Pod

  @tofu @op:runTrigger @case:failure
  Scenario: A run whose runner image cannot be pulled, or whose pool selects nodes of another architecture, fails in PREPARING and leaves no Pod
    Given token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    And the Managed WorkerPool "elsewhere" of capacity 1 in the space "team", whose Pods select "s390x" nodes, is applied in "home"
    And the WorkerPool "elsewhere" eventually has 1 registered slots, its pool kept as "elsewhere"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Missing","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}","workerPool":"{build}","runnerImage":"ghcr.io/weaveplatform/infraweave-runner-opentofu@sha256:0000000000000000000000000000000000000000000000000000000000000000","vendorConfig":{"opentofu":{}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Far","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}","workerPool":"{elsewhere}","vendorConfig":{"opentofu":{}}},"manageState":true}' through A
    When token "h" sends "POST /api/v1/stacks/missing/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "missing"
    And token "h" sends "POST /api/v1/stacks/far/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "far"
    Then token "h" eventually reads the run "missing" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note contains 'cannot be pulled'
    And the run "missing" eventually has no Pod
    And token "h" eventually reads the run "far" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note contains 'selects s390x nodes'
    And the run "far" eventually has no Pod
