@postgres @objectstore @vcs @docker
Feature: A Docker launcher's phase-scoped inputs reach only the phases they are scoped to
  A real launcher passes the variables of its own environment that
  launcher.whitelist_envs names, and the host files its Docker extra
  mounts name, to its runs by phase. PHASE_TOKEN is ro_PHASE_TOKEN's
  read-t0ken in initialization and planning and wo_PHASE_TOKEN's
  write-t0ken in applying, after_run and every phase of a task, never
  under its prefixed name; PHASE_CACHE, /opt/cache, reaches every phase
  unless a stack variable of that name takes precedence. The host file of
  ro_extra_mounts is /mnt/keys/read.txt only while the read set is
  exposed, wo_extra_mounts' /mnt/keys/write.txt only while the write set
  is, and extra_mounts' /mnt/shared/shared.txt always. A write is refused
  everywhere: the shared file is a read-only bind mount, and the runner
  writes the scoped files, which the handoff alone carries, read-only in
  an in-memory directory it leaves unwritable, removing the other side's
  before any hook of a phase. mask_envs names the write token, which the
  log holds as five asterisks. The run's container is labelled with the
  vendor and tool it executes, which the launcher logs with its runtime
  and the tool's verified release, and neither carries an input. An
  initialization command helper receives none of them. The repository's
  probe.sh prints what its phase sees without printing a scoped file. The
  release indexes serve the recorded fixtures; the home key is
  space-admin on the account's top space.
  ref: decision 0008
  ref: decision 0013

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf  | resource "terraform_data" "this" {\n  input = "phase-inputs"\n}\n |
      | probe.sh | p=$1\necho "$p: token=${PHASE_TOKEN:-unset} cache=${PHASE_CACHE:-unset}"\ncase "${PHASE_TOKEN:-}" in read-t0ken) echo "$p: the read token";; write-t0ken) echo "$p: the write token";; esac\nif env \| grep -q '^[rw]o_PHASE'; then echo "$p: a prefixed name"; fi\nfor f in /mnt/keys/read.txt /mnt/keys/write.txt /mnt/shared/shared.txt; do if [ -e $f ]; then echo "$p: has $f"; else echo "$p: lacks $f"; fi; done\nif grep -qx read-file /mnt/keys/read.txt 2>/dev/null; then echo "$p: read.txt holds the read file"; fi\nif grep -qx write-f1le /mnt/keys/write.txt 2>/dev/null; then echo "$p: write.txt holds the write file"; fi\nif grep -qx shared-file /mnt/shared/shared.txt 2>/dev/null; then echo "$p: shared.txt holds the shared file"; fi\nfor f in /mnt/keys/read.txt /mnt/keys/write.txt /mnt/keys/new.txt /mnt/shared/shared.txt /mnt/shared/new.txt; do if e=$( (echo x > $f) 2>&1 ); then echo "$p: wrote $f"; else echo "$p: refused $f: ${e##*: }"; fi; done\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the launchers started from now on receive the phase-scoped inputs

  @tofu @op:runTrigger @op:runConfirm @case:success
  Scenario: A tracked OpenTofu run plans with the read set and applies with the write set in the container that restores its workspace
    Given token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}},"beforeInit":["sh probe.sh init"],"beforePlan":["sh probe.sh plan"],"beforeApply":["sh probe.sh apply"],"afterRun":["sh probe.sh after_run"]},"manageState":true}' through B
    And the relay holds the runners' reports
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then the relay holds a report of the run "run"
    And the run "run"'s container executes the vendor "opentofu" with the tool "tofu" and carries no phase-scoped value
    When the relay releases the runners' reports
    Then token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And token "h" reads the run "run"'s INITIALIZING log through B, which holds "init: token=read-t0ken cache=/opt/cache\ninit: the read token\ninit: has /mnt/keys/read.txt" and not "write-t0ken"
    And token "h" reads the run "run"'s PLANNING log through A, which holds "plan: token=read-t0ken cache=/opt/cache\nplan: the read token\nplan: has /mnt/keys/read.txt\nplan: lacks /mnt/keys/write.txt\nplan: has /mnt/shared/shared.txt\nplan: read.txt holds the read file\nplan: shared.txt holds the shared file\nplan: refused /mnt/keys/read.txt: Permission denied\nplan: refused /mnt/keys/write.txt: Permission denied\nplan: refused /mnt/keys/new.txt: Permission denied\nplan: refused /mnt/shared/shared.txt: Read-only file system\nplan: refused /mnt/shared/new.txt: Permission denied" and not "write-t0ken"
    And token "h" reads the run "run"'s PLANNING log through B, which holds "infraweave: saved the workspace for the plan's confirmation" and not "a prefixed name"
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through A
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, CONFIRMED, PREPARING_APPLY, APPLYING, FINISHED"
    And token "h" reads the run "run"'s APPLYING log through A, which holds "init: token=***** cache=/opt/cache\ninit: the write token\ninit: lacks /mnt/keys/read.txt" and not "read-t0ken"
    And token "h" reads the run "run"'s APPLYING log through B, which holds "apply: token=***** cache=/opt/cache\napply: the write token\napply: lacks /mnt/keys/read.txt\napply: has /mnt/keys/write.txt\napply: has /mnt/shared/shared.txt\napply: write.txt holds the write file\napply: shared.txt holds the shared file\napply: refused /mnt/keys/read.txt: Permission denied\napply: refused /mnt/keys/write.txt: Permission denied\napply: refused /mnt/keys/new.txt: Permission denied\napply: refused /mnt/shared/shared.txt: Read-only file system\napply: refused /mnt/shared/new.txt: Permission denied" and not "write-t0ken"
    And the launcher "l" logged the run "run"'s runner output "after_run: token=***** cache=/opt/cache\nafter_run: the write token\nafter_run: lacks /mnt/keys/read.txt"
    And token "h" reads the run "run"'s APPLYING log through B, which holds "Apply complete! Resources: 1 added, 0 changed, 0 destroyed." and not "wrote /mnt"
    And the launcher "l" logged the run "run"'s container on the runtime "docker" executing the vendor "opentofu" with the tool "tofu" of the release, and no phase-scoped value
    And no answer of the scenario, log line of A or B, audit entry or event holds "write-t0ken"

  @terraform @op:runTrigger @case:success
  Scenario: An autodeployed Terraform run switches from the read set to the write set in one container, under a stack variable's precedence
    Given token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"terraform":{}},"beforePlan":["sh probe.sh plan"],"beforeApply":["sh probe.sh apply"],"afterRun":["sh probe.sh after_run"]},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"PHASE_CACHE","type":"ENVIRONMENT_VARIABLE","value":"/stack/cache","writeOnly":false}' through A
    And the relay holds the runners' reports
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then the relay holds a report of the run "run"
    And the run "run"'s container executes the vendor "terraform" with the tool "terraform" and carries no phase-scoped value
    When the relay releases the runners' reports
    Then token "h" eventually reads the run "run" in the state "FINISHED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, APPLYING, FINISHED"
    And token "h" reads the run "run"'s PLANNING log through B, which holds "plan: token=read-t0ken cache=/stack/cache\nplan: the read token\nplan: has /mnt/keys/read.txt\nplan: lacks /mnt/keys/write.txt\nplan: has /mnt/shared/shared.txt\nplan: read.txt holds the read file\nplan: shared.txt holds the shared file\nplan: refused /mnt/keys/read.txt: Permission denied\nplan: refused /mnt/keys/write.txt: Permission denied\nplan: refused /mnt/keys/new.txt: Permission denied\nplan: refused /mnt/shared/shared.txt: Read-only file system\nplan: refused /mnt/shared/new.txt: Permission denied" and not "write-t0ken"
    And token "h" reads the run "run"'s APPLYING log through A, which holds "apply: token=***** cache=/stack/cache\napply: the write token\napply: lacks /mnt/keys/read.txt\napply: has /mnt/keys/write.txt\napply: has /mnt/shared/shared.txt\napply: write.txt holds the write file\napply: shared.txt holds the shared file\napply: refused /mnt/keys/read.txt: Permission denied\napply: refused /mnt/keys/write.txt: Permission denied\napply: refused /mnt/keys/new.txt: Permission denied\napply: refused /mnt/shared/shared.txt: Read-only file system\napply: refused /mnt/shared/new.txt: Permission denied" and not "write-t0ken"
    And the launcher "l" logged the run "run"'s runner output "after_run: token=***** cache=/stack/cache\nafter_run: the write token\nafter_run: lacks /mnt/keys/read.txt"
    And the launcher "l" logged the run "run"'s container on the runtime "docker" executing the vendor "terraform" with the tool "terraform" of the release, and no phase-scoped value

  @tofu @worker-initialization @op:taskCreate @case:success
  Scenario: A task's every phase has the write set, and its initialization command helper none
    Given token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    And the launchers started from now on evaluate a REGO_V1 initialization policy:
      """
      package infraweave
      import future.keywords
      probe := exec(["sh", "-c", "env; ls -A /mnt/keys /mnt/shared 2>&1; true"])
      deny contains "Helper probe failed" if { probe.status != 0 }
      deny contains "Helper saw a phase-scoped variable" if { contains(probe.stdout, "PHASE_") }
      deny contains "Helper saw a phase-scoped value" if { contains(probe.stdout, "t0ken") }
      deny contains "Helper saw a scoped file" if { contains(probe.stdout, "read.txt") }
      deny contains "Helper saw a scoped file" if { contains(probe.stdout, "write.txt") }
      deny contains "Helper saw the shared file" if { contains(probe.stdout, "shared.txt") }
      """
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}},"beforeInit":["sh probe.sh init"],"afterRun":["sh probe.sh after_run"]},"manageState":true}' through B
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"sh probe.sh task"}' through A
    Then it answers 201
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PERFORMING, FINISHED"
    And token "h" reads the run "run"'s INITIALIZING log through A, which holds "init: token=***** cache=/opt/cache\ninit: the write token\ninit: lacks /mnt/keys/read.txt" and not "read-t0ken"
    And token "h" reads the run "run"'s PERFORMING log through B, which holds "task: token=***** cache=/opt/cache\ntask: the write token\ntask: lacks /mnt/keys/read.txt\ntask: has /mnt/keys/write.txt\ntask: has /mnt/shared/shared.txt\ntask: write.txt holds the write file\ntask: shared.txt holds the shared file\ntask: refused /mnt/keys/read.txt: Permission denied\ntask: refused /mnt/keys/write.txt: Permission denied\ntask: refused /mnt/keys/new.txt: Permission denied\ntask: refused /mnt/shared/shared.txt: Read-only file system\ntask: refused /mnt/shared/new.txt: Permission denied" and not "write-t0ken"
    And the launcher "l" logged the run "run"'s runner output "after_run: token=***** cache=/opt/cache\nafter_run: the write token"
    And the run "run" eventually has no initialization command container
