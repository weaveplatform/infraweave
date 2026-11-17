@postgres @objectstore @vcs @docker
Feature: A task force-unlocks a managed state workspace
  Terraform's empty-body UNLOCK releases every lock, while OpenTofu names
  the lock it breaks. Only a caller allowed the exclusive lock may break
  one. The release records its task caller, workspace and complete
  LockInfo on the audit trail, and one broken-lock row per released lock.
  The former holder then learns through 423 that it cannot release or
  write under the broken lock. Locks are seeded as a stopped server could
  leave them: no live run leaves a lock after its terminal event is read.
  The scenario acts as two private-pool workers, running the pinned tools
  and the actual runner in Docker against Gitea and the state relay.
  ref: decision 0010
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And the launcher "w" registers with the key of "csr" and the certificate "cert" through A
    And the launcher "w2" registers with the key of "csr" and the certificate "cert" through B

  @op:taskCreate @op:searchTofuWorkspaces @case:success
  Scenario Outline: A task breaks the seeded locks, and their former holder cannot release or write under them
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"<vendor>":{}}},"manageState":true}' through B
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And the answer's id names "holder"
    And token "h" eventually reads the run "holder" in the state "READY" through B
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through A
    And the answer assigns the run "holder", its run token kept as "rt"
    And the worker takes over the run "holder" with token "rt" and the key of "csr", checking out "infra", through A
    And the worker reports the run "holder" started in INITIALIZING through A
    And <tool> "init" of the run "holder" exits 0
    And the worker reports the run "holder" succeeded in INITIALIZING through B
    And <locks> stopped-server locks of the run "holder" are seeded on its default workspace
    And token "h" sends "POST /api/v1/tofu-workspaces/search" with '{"predicates":[{"field":"stack","constraint":{"stringEquals":["infra"]}}]}' through A
    Then the answer's Tofu workspaces are "default locked by the home key"
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"<tool> force-unlock -force {lock}"}' through A
    Then it answers 201
    And the answer's id names "task"
    And token "h" eventually reads the run "task" in the state "READY" through B
    When token "w2" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "task", its run token kept as "tt"
    And the worker takes over the run "task" with token "tt" and the key of "csr", checking out "infra", through A
    And the runner executes the run "task" with <tool> and exits 0
    Then token "h" eventually reads the run "task" in the state "FINISHED" through B
    And the relay passed an UNLOCK of the state of the run "task" answered 200
    When token "h" sends "POST /api/v1/tofu-workspaces/search" with '{"predicates":[{"field":"stack","constraint":{"stringEquals":["infra"]}}]}' through B
    Then the answer's Tofu workspaces are "default"
    And the seeded locks are recorded as broken
    And the task "task" records one release of exactly the seeded LockInfo
    When the former holder "holder" is arranged as a writing caller
    And the run "holder" sends its own UNLOCK through A
    Then it answers 423
    When the run "holder" sends a write through B
    Then it answers 423
    And the run "holder" wrote 0 state versions

    @terraform
    Examples: Terraform's empty body releases every seeded lock
      | tool      | vendor    | locks |
      | terraform | terraform | 2     |

    @tofu
    Examples: OpenTofu names the lock it releases
      | tool | vendor   | locks |
      | tofu | opentofu | 1     |

  @case:denial
  Scenario Outline: A proposed run's empty-body unlock is refused and leaves the lock held
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"<vendor>":{}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And the answer's id names "holder"
    And token "h" eventually reads the run "holder" in the state "READY" through B
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through A
    And the answer assigns the run "holder", its run token kept as "rt"
    And the worker takes over the run "holder" with token "rt" and the key of "csr", checking out "infra", through A
    And the worker reports the run "holder" started in INITIALIZING through A
    And <tool> "init" of the run "holder" exits 0
    And the worker reports the run "holder" succeeded in INITIALIZING through B
    And 1 stopped-server lock of the run "holder" are seeded on its default workspace
    And the run "holder" sends an empty-body UNLOCK through B
    Then it answers a 403 problem "forbidden"
    And the seeded locks remain held
    And the workspace has no broken-lock records
    And the task "holder" records no release of exactly the seeded LockInfo
    When token "h" sends "POST /api/v1/tofu-workspaces/search" with '{"predicates":[{"field":"stack","constraint":{"stringEquals":["infra"]}}]}' through A
    Then the answer's Tofu workspaces are "default locked by the home key"

    @terraform
    Examples: Terraform
      | tool      | vendor    |
      | terraform | terraform |

    @tofu
    Examples: OpenTofu
      | tool | vendor   |
      | tofu | opentofu |

  @op:taskCreate @case:failure
  Scenario Outline: Force-unlock on an unlocked workspace releases nothing and records no break
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"<vendor>":{}}},"manageState":true}' through B
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    When token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"<tool> force-unlock -force 11111111-1111-4111-8111-000000000001"}' through A
    Then it answers 201
    And the answer's id names "task"
    And token "h" eventually reads the run "task" in the state "READY" through B
    When token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through A
    And the answer assigns the run "task", its run token kept as "tt"
    And the worker takes over the run "task" with token "tt" and the key of "csr", checking out "infra", through A
    And the runner executes the run "task" with <tool> and exits 0
    Then token "h" eventually reads the run "task" in the state "FINISHED" through B
    And the relay passed an UNLOCK of the state of the run "task" answered 200
    And the workspace has no broken-lock records
    And the task "task" records no release of exactly the seeded LockInfo
    When token "h" sends "POST /api/v1/tofu-workspaces/search" with '{"predicates":[{"field":"stack","constraint":{"stringEquals":["infra"]}}]}' through B
    Then the answer's Tofu workspaces are "default"

    @terraform
    Examples: Terraform
      | tool      | vendor    |
      | terraform | terraform |

    @tofu
    Examples: OpenTofu
      | tool | vendor   |
      | tofu | opentofu |
