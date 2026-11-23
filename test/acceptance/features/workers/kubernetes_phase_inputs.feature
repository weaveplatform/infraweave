@kubernetes-lab @kubernetes @postgres @vcs
Feature: A WorkerPool's phase-scoped inputs reach only the phases they are scoped to
  A real worker controller resolves its Managed pool's environment and
  mounts, which name keys of a Secret and a ConfigMap in the pool's own
  namespace, for each run its slot claims, and its launcher executes the
  run in Pods. PHASE_TOKEN is the Secret's read token, read-t0ken, in
  initialization and planning and its write token, write-t0ken, in
  applying and after_run, never under its ro_ or wo_ name, and the log
  holds either as five asterisks because a Secret holds it; PHASE_CACHE,
  the ConfigMap's /opt/cache, reaches every phase. The Secret's read.txt
  is /mnt/keys/read.txt only while the read set is exposed and its
  write.txt /mnt/keys/write.txt only while the write set is; the
  ConfigMap's shared.txt is /mnt/shared/shared.txt in every phase. A write
  is refused everywhere: the shared file is a read-only volume mount, and
  the runner writes the scoped files, which the handoff alone carries,
  read-only in a Pod-local in-memory directory it owns and leaves
  unwritable, removing the other side's before any hook of a phase. Every
  Pod is labelled with the vendor and tool it executes, which the
  controller logs with its runtime, and no Pod, Worker resource or
  controller log line holds an input's value. The repository's probe.sh
  prints what its phase sees without printing a scoped file. Repositories
  are Gitea's; the home key is space-admin on the account's top space.
  ref: decision 0008
  ref: decision 0013

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
      | main.tf  | resource "terraform_data" "this" {\n  input = "phase-inputs"\n}\n |
      | probe.sh | p=$1\necho "$p: token=${PHASE_TOKEN:-unset} cache=${PHASE_CACHE:-unset}"\ncase "${PHASE_TOKEN:-}" in read-t0ken) echo "$p: the read token";; write-t0ken) echo "$p: the write token";; esac\nif env \| grep -q '^[rw]o_PHASE'; then echo "$p: a prefixed name"; fi\nfor f in /mnt/keys/read.txt /mnt/keys/write.txt /mnt/shared/shared.txt; do if [ -e $f ]; then echo "$p: has $f"; else echo "$p: lacks $f"; fi; done\nif grep -qx read-file /mnt/keys/read.txt 2>/dev/null; then echo "$p: read.txt holds the read file"; fi\nif grep -qx write-f1le /mnt/keys/write.txt 2>/dev/null; then echo "$p: write.txt holds the write file"; fi\nif grep -qx shared-file /mnt/shared/shared.txt 2>/dev/null; then echo "$p: shared.txt holds the shared file"; fi\nfor f in /mnt/keys/read.txt /mnt/keys/write.txt /mnt/keys/new.txt /mnt/shared/shared.txt /mnt/shared/new.txt; do if e=$( (echo x > $f) 2>&1 ); then echo "$p: wrote $f"; else echo "$p: refused $f: ${e##*: }"; fi; done\n |
    And the head of "main" of "infra" is kept as "seed"
    And 1 worker controller watch the controller namespaces
    And the Managed WorkerPool "build" of capacity 1 in the space "team", with the phase-scoped inputs, is applied in "home"
    And the WorkerPool "build" eventually has 1 registered slots, its pool kept as "build"
    And the WorkerPool "build" eventually reports ExecutorAvailable True "LauncherRunning"

  @tofu @op:runTrigger @op:runConfirm @case:success
  Scenario: A tracked OpenTofu run plans with the read set in one Pod and applies with the write set in the Pod that restores its workspace
    Given token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}","workerPool":"{build}","vendorConfig":{"opentofu":{}},"beforeInit":["sh probe.sh init"],"beforePlan":["sh probe.sh plan"],"beforeApply":["sh probe.sh apply"],"afterRun":["sh probe.sh after_run"]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And token "h" reads the run "run"'s INITIALIZING log through B, which holds "init: token=***** cache=/opt/cache\ninit: the read token\ninit: has /mnt/keys/read.txt" and not "read-t0ken"
    And token "h" reads the run "run"'s PLANNING log through A, which holds "plan: token=***** cache=/opt/cache\nplan: the read token\nplan: has /mnt/keys/read.txt\nplan: lacks /mnt/keys/write.txt\nplan: has /mnt/shared/shared.txt\nplan: read.txt holds the read file\nplan: shared.txt holds the shared file\nplan: refused /mnt/keys/read.txt: Permission denied\nplan: refused /mnt/keys/write.txt: Permission denied\nplan: refused /mnt/keys/new.txt: Permission denied\nplan: refused /mnt/shared/shared.txt: Read-only file system\nplan: refused /mnt/shared/new.txt: Permission denied" and not "read-t0ken"
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through A
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, CONFIRMED, PREPARING_APPLY, APPLYING, FINISHED"
    And token "h" reads the run "run"'s APPLYING log through A, which holds "init: token=***** cache=/opt/cache\ninit: the write token\ninit: lacks /mnt/keys/read.txt" and not "write-t0ken"
    And token "h" reads the run "run"'s APPLYING log through B, which holds "apply: token=***** cache=/opt/cache\napply: the write token\napply: lacks /mnt/keys/read.txt\napply: has /mnt/keys/write.txt\napply: has /mnt/shared/shared.txt\napply: write.txt holds the write file\napply: shared.txt holds the shared file\napply: refused /mnt/keys/read.txt: Permission denied\napply: refused /mnt/keys/write.txt: Permission denied\napply: refused /mnt/keys/new.txt: Permission denied\napply: refused /mnt/shared/shared.txt: Read-only file system\napply: refused /mnt/shared/new.txt: Permission denied" and not "write-t0ken"
    And a worker controller logged the run "run"'s runner output "after_run: token=***** cache=/opt/cache\nafter_run: the write token\nafter_run: lacks /mnt/keys/read.txt"
    And token "h" reads the run "run"'s APPLYING log through B, which holds "Apply complete! Resources: 1 added, 0 changed, 0 destroyed." and not "wrote /mnt"
    And the run "run" ran in Pods of the WorkerPool "build" from the image "{runner.opentofu}", each hardened and holding no "write-t0ken"
    And the run "run" ran in Pods labelled with the vendor "opentofu" and the tool "tofu"
    And a worker controller logged the run "run"'s container on the runtime "kubernetes" executing the vendor "opentofu" with the tool "tofu" of the release, and no phase-scoped value
    And the run "run" eventually has no Pod
    And no run Pod, Worker resource or worker controller log line holds "read-t0ken"
    And no run Pod, Worker resource or worker controller log line holds "write-f1le"
    And no answer of the scenario, log line of A or B, audit entry or event holds "write-t0ken"

  @terraform @op:runTrigger @case:success
  Scenario: An autodeployed Terraform run switches from the read set to the write set in one Pod
    Given token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}","workerPool":"{build}","autodeploy":true,"vendorConfig":{"terraform":{}},"beforePlan":["sh probe.sh plan"],"beforeApply":["sh probe.sh apply"]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, APPLYING, FINISHED"
    And token "h" reads the run "run"'s PLANNING log through A, which holds "plan: token=***** cache=/opt/cache\nplan: the read token\nplan: has /mnt/keys/read.txt\nplan: lacks /mnt/keys/write.txt\nplan: has /mnt/shared/shared.txt\nplan: read.txt holds the read file\nplan: shared.txt holds the shared file\nplan: refused /mnt/keys/read.txt: Permission denied\nplan: refused /mnt/keys/write.txt: Permission denied\nplan: refused /mnt/keys/new.txt: Permission denied\nplan: refused /mnt/shared/shared.txt: Read-only file system\nplan: refused /mnt/shared/new.txt: Permission denied" and not "read-t0ken"
    And token "h" reads the run "run"'s APPLYING log through B, which holds "apply: token=***** cache=/opt/cache\napply: the write token\napply: lacks /mnt/keys/read.txt\napply: has /mnt/keys/write.txt\napply: has /mnt/shared/shared.txt\napply: write.txt holds the write file\napply: shared.txt holds the shared file\napply: refused /mnt/keys/read.txt: Permission denied\napply: refused /mnt/keys/write.txt: Permission denied\napply: refused /mnt/keys/new.txt: Permission denied\napply: refused /mnt/shared/shared.txt: Read-only file system\napply: refused /mnt/shared/new.txt: Permission denied" and not "write-t0ken"
    And the run "run" ran in Pods of the WorkerPool "build" from the image "{runner.terraform}", each hardened and holding no "read-t0ken"
    And the run "run" ran in Pods labelled with the vendor "terraform" and the tool "terraform"
    And a worker controller logged the run "run"'s container on the runtime "kubernetes" executing the vendor "terraform" with the tool "terraform" of the release, and no phase-scoped value
    And the run "run" eventually has no Pod
