@postgres @objectstore @vcs
Feature: Pool queues list the runs waiting for a worker
  A queue lists the caller's account's runs a worker may claim, on the
  stacks in the spaces the caller may read, each at its position in the
  claim's order among those runs: a private pool by priority, then
  creation; the public pool by creation alone. A position is read when
  the search is, and reserves nothing. The public pool's busy workers are
  the account's runs a public worker holds.
  ref: decision 0008

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database, with an operator key and the settings '{"worker":{"public_pool":{"enabled":true}}}'
    And the operator key is exchanged through A for token "op"
    And the home key is exchanged through B for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And a certificate signing request for a P-256 key is kept as "csr"
    And token "op" sends "POST /operator/v1/public-worker-pool" with '{"certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "public"
    And the answer's pool token names the pool "public" and chains to the pool CA, its serial kept as "publiccert"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"

  @op:workerPoolSearchSchedulableRuns @op:workerPoolSearchSchedulableRunsSuggestions @op:publicWorkerPoolSearchSchedulableRuns @op:publicWorkerPoolSearchSchedulableRunsSuggestions @op:publicWorkerPool @op:workerPool @case:success
  Scenario: Each queue lists its waiting runs in claim order, and the public pool counts the runs its workers hold
    Given token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Other","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Open","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Secret","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}","vendorConfig":{"terraform":{}}},"manageState":true}' through A
    And the member key is space-reader on the space "legacy"
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "first"
    And token "h" eventually reads the run "first" in the state "READY" through A
    And token "h" sends "POST /api/v1/stacks/other/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "second"
    And token "h" eventually reads the run "second" in the state "READY" through B
    And token "h" sends "POST /api/v1/stacks/open/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "pub"
    And token "h" eventually reads the run "pub" in the state "READY" through A
    And token "h" sends "POST /api/v1/stacks/secret/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "hidden"
    And token "h" eventually reads the run "hidden" in the state "READY" through B
    When token "h" sends "GET /api/v1/worker-pools/{pool}" through A
    Then the answer's pendingRuns is '2'
    And the answer's schedulableRunsCount is '2'
    When token "h" sends "GET /api/v1/worker-pools/{pool}/search-schedulable-runs?input=%7B%7D" through B
    Then the answer lists the queued runs "first@1, second@2"
    When token "h" sends "GET /api/v1/worker-pools/{pool}/search-schedulable-runs?input=%7B%22predicates%22%3A%5B%7B%22field%22%3A%22stackName%22%2C%22constraint%22%3A%7B%22stringMatches%22%3A%5B%22Other%22%5D%7D%7D%5D%7D" through A
    Then the answer lists the queued runs "second@2"
    When token "h" sends "GET /api/v1/worker-pools/{pool}/search-schedulable-runs-suggestions?input=%7B%22fields%22%3A%5B%22state%22%5D%7D" through B
    Then the answer suggests the state values "READY:2" of 2 runs
    When token "h" sends "GET /api/v1/public-worker-pool" through A
    Then the answer's pendingRuns is '2'
    And the answer's schedulableRunsCount is '2'
    And the answer's busyWorkers is '0'
    When token "h" sends "GET /api/v1/worker-pools/public/search-schedulable-runs?input=%7B%7D" through B
    Then the answer lists the queued runs "pub@1, hidden@2"
    When token "h" sends "GET /api/v1/worker-pools/public/search-schedulable-runs-suggestions?input=%7B%22fields%22%3A%5B%22stackName%22%5D%7D" through A
    Then the answer suggests the stackName values "Open:1, Secret:1" of 2 runs
    When token "m" sends "GET /api/v1/public-worker-pool" through B
    Then the answer's pendingRuns is '1'
    When token "m" sends "GET /api/v1/worker-pools/public/search-schedulable-runs?input=%7B%7D" through A
    Then the answer lists the queued runs "pub@1"
    When the public launcher "probe" registers with the key of "csr" and the certificate "publiccert" through B
    Then it answers 200
    When token "probe" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then the answer assigns the run "pub", its run token kept as "rt"
    When token "h" sends "GET /api/v1/public-worker-pool" through A
    Then the answer's busyWorkers is '1'
    And the answer's pendingRuns is '1'
    When token "h" sends "GET /api/v1/worker-pools/public/search-schedulable-runs?input=%7B%7D" through B
    Then the answer lists the queued runs "hidden@1"
    When token "m" sends "GET /api/v1/public-worker-pool" through A
    Then the answer's busyWorkers is '1'
    And the answer's pendingRuns is '0'
