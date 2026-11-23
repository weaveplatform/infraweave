@kubernetes-lab @kubernetes @postgres @vcs @tofu
Feature: Kubernetes run recovery
  Two real worker controllers, as separate processes, host a Managed
  pool's slot in the owned cluster; one leads its Lease and the other
  stands by. Each slot's launcher records its assignment, its kill intent
  and its runner's exit in its Worker resource before acting, so the
  controller that next leads, after the leader is killed outright, adopts
  the run Pod the Worker controls and reports its one exit, never
  creating another Pod or adopting one the Worker does not own. A run Pod
  deleted under its runner fails the run as "container missing", and one
  evicted through the Eviction API as "container evicted", without the
  run moving to another pool. A preparation is bounded by the preparing
  deadline its claim issued, and a pending Pod the new leader finds
  without its handoff is deleted and its run failed. A drained worker
  finishes its run, through a lost connection to the installation, and
  claims no other until it is undrained; while it holds the run its
  availableAt is absent, and once it reports itself idle availableAt is no
  earlier than the read that saw it busy. Run Pods reach the servers only
  through the scenario's HTTPS front. The home key is space-admin on the
  account's top space.
  ref: decision 0008
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
      | main.tf | resource "terraform_data" "this" {\n  input = "one"\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And 2 worker controllers watch the controller namespaces
    And the Managed WorkerPool "build" of capacity 1 in the space "team" is applied in "home"
    And the WorkerPool "build" eventually has 1 registered slots, its pool kept as "build"
    And the WorkerPool "build" eventually reports ExecutorAvailable True "LauncherRunning"
    And token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first

  @op:runTrigger @case:success
  Scenario: The next leader adopts a run's Pod after the leader is killed, and the run finishes once
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Slow","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}","workerPool":"{build}","autodeploy":true,"vendorConfig":{"opentofu":{}},"beforePlan":["sleep 40"]},"manageState":true}' through B
    And a Pod of the WorkerPool "build"'s worker that no Worker owns is created as "foreign"
    When token "h" sends "POST /api/v1/stacks/slow/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    And the run "run" eventually has a handed-off Pod
    And the leading worker controller is killed
    Then token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, APPLYING, FINISHED"
    And the run "run" wrote 1 state version
    And the run "run" ran in 1 Pod
    And the run "run" eventually has no Pod
    And the Worker of the WorkerPool "build" eventually reports Connected True "Polling"
    And the Pod "foreign" remains

  @op:runTrigger @case:failure
  Scenario: A run whose Pod is deleted fails as missing, and one whose Pod is evicted fails as evicted, each on its own pool
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Long","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}","workerPool":"{build}","vendorConfig":{"opentofu":{}},"beforePlan":["sleep 600"]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/long/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "gone"
    And the run "gone" eventually has a handed-off Pod
    And the run "gone"'s Pod is deleted
    Then token "h" eventually reads the run "gone" in the state "FAILED" through B
    And the answer's last state transition's note contains 'container missing'
    And the run "gone" ran in 1 Pod
    And the run "gone" eventually has no Pod
    When token "h" sends "POST /api/v1/stacks/long/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "evicted"
    And the run "evicted" eventually has a handed-off Pod
    And the run "evicted"'s Pod is evicted
    Then token "h" eventually reads the run "evicted" in the state "FAILED" through A
    And the answer's last state transition's note contains 'container evicted'
    And the run "evicted" ran in 1 Pod
    And the run "evicted" eventually has no Pod

  @op:runTrigger @case:failure
  Scenario: A preparation that outlasts its issued deadline, or that the leader's kill interrupts, fails in PREPARING and leaves no Pod
    Given the Managed WorkerPool "late" of capacity 1 in the space "team", whose Pods no node accepts, preparing for 45 seconds, is applied in "home"
    And the Managed WorkerPool "stuck" of capacity 1 in the space "team", whose Pods no node accepts, is applied in "home"
    And the WorkerPool "late" eventually has 1 registered slots, its pool kept as "late"
    And the WorkerPool "stuck" eventually has 1 registered slots, its pool kept as "stuck"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Late","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}","workerPool":"{late}","vendorConfig":{"opentofu":{}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Stuck","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}","workerPool":"{stuck}","vendorConfig":{"opentofu":{}}},"manageState":true}' through A
    When token "h" sends "POST /api/v1/stacks/late/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "late"
    Then the run "late" eventually has a pending Pod
    And token "h" eventually reads the run "late" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note contains 'preparing'
    And the run "late" eventually has no Pod
    When token "h" sends "POST /api/v1/stacks/stuck/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "cut"
    And the run "cut" eventually has a pending Pod
    And the leading worker controller is killed
    Then token "h" eventually reads the run "cut" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note contains 'container missing'
    And the run "cut" ran in 1 Pod
    And the run "cut" eventually has no Pod

  @op:workerDrainSet @op:runTrigger @case:success
  Scenario: A drained worker finishes its run through a lost connection, and claims no other until it is undrained
    Given the worker of the WorkerPool "build" is kept as "w"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Slow","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}","workerPool":"{build}","autodeploy":true,"vendorConfig":{"opentofu":{}},"beforePlan":["echo the hook waits","sleep 75"]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/slow/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "first"
    And the run "first" eventually has a handed-off Pod
    And token "h" eventually reads the worker "w" of the pool "build" busy, with no availableAt, through A
    And token "h" sends "PUT /api/v1/worker-pools/{build}/workers/{w}/drain" with '{"drain":true}' through B
    Then it answers 200
    And the Worker of the WorkerPool "build" eventually reports Drained True "Drained"
    And a worker controller eventually logs the runner output "the hook waits"
    When the HTTPS front answers 502 for 30 seconds
    Then token "h" eventually reads the run "first" in the state "FINISHED" through A
    And the run "first" ran in 1 Pod
    And token "h" eventually reads the worker "w" of the pool "build" as "DRAINED", available since it was read busy, through B
    When token "h" sends "POST /api/v1/stacks/slow/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "second"
    Then the run "second" gets no Pod for 20 seconds
    When token "h" sends "PUT /api/v1/worker-pools/{build}/workers/{w}/drain" with '{"drain":false}' through A
    Then it answers 200
    And the Worker of the WorkerPool "build" eventually reports Drained False "AcceptingRuns"
    And token "h" eventually reads the run "second" in the state "FINISHED" through B
    And the run "second" ran in 1 Pod
