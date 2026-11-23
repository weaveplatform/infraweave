@postgres
Feature: Pool projections count what the caller's account may read
  A pool's users are the stacks that select it: a private pool's ID, or no
  private pool for the public pool. Every count and search reads only the
  caller's account's stacks in the spaces the caller may read. The public
  pool's parallelism is the account's effective public-worker-slots quota,
  null while billing is off. The usage read answers billing cycles,
  calendar months in UTC, back as far as the samples' retention reaches.
  The literal selector public names the public pool on its own routes
  alone; a private pool's route refuses it. The home key is space-admin on
  the account's top space; the member key holds only the bindings a
  scenario gives it.
  ref: decision 0008

  Background:
    Given two infraweave servers, A and B, on one database, with an operator key and the settings '{"worker":{"public_pool":{"enabled":true,"outgoing_ip_addresses":["203.0.113.7","2001:db8::7"]},"samples":{"retention":"8760h"}}}'
    And the operator key is exchanged through A for token "op"
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through B for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And a certificate signing request for a P-256 key is kept as "csr"

  @op:publicWorkerPool @op:publicWorkerPoolSearchUsers @op:publicWorkerPoolSearchUsersSuggestions @op:workerPoolSearchUsers @op:workerPoolSearchUsersSuggestions @op:workerPool @case:success
  Scenario: Each pool lists and counts the stacks that select it in the spaces the caller reads
    Given token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"provider":"GIT","name":"Infra","repository":"infra","branch":"main","space":"legacy","workerPool":"{pool}"},"manageState":false}' through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"provider":"GIT","name":"Open","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"provider":"GIT","name":"Secret","repository":"infra","branch":"main","space":"{team}"},"manageState":false}' through B
    And the member key is space-reader on the space "legacy"
    When token "h" sends "GET /api/v1/worker-pools/{pool}" through A
    Then the answer's usersCount is '1'
    And the answer lists the pool users "infra"
    And the answer's pendingRuns is '0'
    And the answer's schedulableRunsCount is '0'
    When token "h" sends "GET /api/v1/worker-pools/{pool}/search-users?input=%7B%7D" through B
    Then the answer lists the pool users "infra"
    When token "h" sends "GET /api/v1/worker-pools/{pool}/search-users-suggestions?input=%7B%22fields%22%3A%5B%22name%22%5D%7D" through A
    Then the answer suggests the name values "Infra:1" of 1 stack
    When token "h" sends "GET /api/v1/public-worker-pool" through B
    Then the answer is '{"parallelism":null,"busyWorkers":0,"pendingRuns":0,"schedulableRunsCount":0,"usersCount":2,"intentProjects":[],"intentProjectsCount":0}'
    When token "h" sends "GET /api/v1/worker-pools/public/search-users?input=%7B%22orderBy%22%3A%5B%7B%22field%22%3A%22name%22%7D%5D%7D" through A
    Then the answer lists the pool users "open, secret"
    When token "m" sends "GET /api/v1/public-worker-pool" through A
    Then the answer's usersCount is '1'
    When token "m" sends "GET /api/v1/worker-pools/public/search-users?input=%7B%7D" through B
    Then the answer lists the pool users "open"
    When token "m" sends "GET /api/v1/worker-pools/public/search-users-suggestions?input=%7B%22fields%22%3A%5B%22name%22%5D%7D" through A
    Then the answer suggests the name values "Open:1" of 1 stack
    When token "m" sends "GET /api/v1/worker-pools/{pool}" through B
    Then the answer lists the pool users "infra"
    When the sibling key is exchanged through A for token "s"
    And token "s" sends "GET /api/v1/public-worker-pool" through B
    Then the answer's usersCount is '0'
    When token "s" sends "GET /api/v1/worker-pools/public/search-users?input=%7B%7D" through A
    Then the answer lists the pool users ""
    When token "s" sends "GET /api/v1/worker-pools/{pool}/search-users?input=%7B%7D" through B
    Then it answers a 404 problem "not-found"

  @op:defaultPublicWorkerPoolRunnerImage @op:outgoingIPAddresses @op:workerUsage @op:publicWorkerPool @case:success
  Scenario: The account reads answer its public runner image, the declared egress, its public quota and its usage cycles
    When token "h" sends "GET /api/v1/default-public-worker-pool-runner-image" through A
    Then the answer is the pinned Terraform runner image
    When token "h" sends "POST /api/v1/account/update-default-worker-pool-runner-images" with '{"publicWorkerPoolRunnerImage":"ghcr.io/acme/public:1"}' through B
    Then it answers 200
    When token "h" sends "GET /api/v1/default-public-worker-pool-runner-image" through A
    Then the answer is '"ghcr.io/acme/public:1"'
    When token "m" sends "GET /api/v1/outgoing-ip-addresses" through B
    Then the answer is '["203.0.113.7","2001:db8::7"]'
    When token "h" sends "GET /api/v1/worker-usage" through A
    Then the answer selects the cycle 0 of 6 calendar months
    And the answer's multiAccountContract is 'false'
    And the answer leaves out "contractedLimit"
    And the answer leaves out "burst"
    When token "h" sends "GET /api/v1/worker-usage?input=%7B%22cyclesAgo%22%3A5%7D" through B
    Then the answer selects the cycle 5 of 6 calendar months
    When token "op" gives the home organization the tier "Business" through A
    And token "op" turns billing on through A
    Then "GET /api/v1/public-worker-pool" through B with token "h" eventually answers '{"parallelism":2,"busyWorkers":0,"pendingRuns":0,"schedulableRunsCount":0,"usersCount":0,"intentProjects":[],"intentProjectsCount":0}'
    When token "h" sends "GET /api/v1/worker-usage" through A
    Then the answer's contractedLimit is '3'
    And the answer's multiAccountContract is 'true'

  @op:publicWorkerPool @op:publicWorkerPoolSearchUsers @op:publicWorkerPoolSearchUsersSuggestions @op:publicWorkerPoolSearchSchedulableRuns @op:publicWorkerPoolSearchSchedulableRunsSuggestions @op:defaultPublicWorkerPoolRunnerImage @op:outgoingIPAddresses @op:workerUsage @op:workerPoolSearchUsers @op:workerPoolSearchUsersSuggestions @op:workerPoolSearchSchedulableRuns @op:workerPoolSearchSchedulableRunsSuggestions @case:denial
  Scenario: A tier without the pool and account capabilities is refused every projection
    Given token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "op" sends "POST /operator/v1/tiers" with '{"name":"Bare","features":["organization"],"limits":{}}' through A
    And it answers 201
    And token "op" gives the home organization the tier "Bare" through A
    And token "op" turns billing on through A
    Then "GET /api/v1/public-worker-pool" through B with token "h" eventually answers a 403 problem "feature-not-in-tier"
    And the problem names the feature "worker-pools"
    When token "h" sends "GET /api/v1/worker-pools/public/search-users?input=%7B%7D" through A
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "GET /api/v1/worker-pools/public/search-users-suggestions?input=%7B%7D" through B
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "GET /api/v1/worker-pools/public/search-schedulable-runs?input=%7B%7D" through A
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "GET /api/v1/worker-pools/public/search-schedulable-runs-suggestions?input=%7B%7D" through B
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "GET /api/v1/outgoing-ip-addresses" through A
    Then it answers a 403 problem "feature-not-in-tier"
    And the problem names the feature "worker-pools"
    When token "h" sends "GET /api/v1/default-public-worker-pool-runner-image" through B
    Then it answers a 403 problem "feature-not-in-tier"
    And the problem names the feature "account-details"
    When token "h" sends "GET /api/v1/worker-usage" through A
    Then it answers a 403 problem "feature-not-in-tier"
    And the problem names the feature "private-worker-pools"
    When token "h" sends "GET /api/v1/worker-pools/{pool}/search-users?input=%7B%7D" through B
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "GET /api/v1/worker-pools/{pool}/search-users-suggestions?input=%7B%7D" through A
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "GET /api/v1/worker-pools/{pool}/search-schedulable-runs?input=%7B%7D" through B
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "GET /api/v1/worker-pools/{pool}/search-schedulable-runs-suggestions?input=%7B%7D" through A
    Then it answers a 403 problem "feature-not-in-tier"

  @op:workerPoolSearchUsers @op:workerPoolSearchSchedulableRuns @op:workerPoolSearchUsersSuggestions @op:workerPoolSearchSchedulableRunsSuggestions @case:denial
  Scenario: A private pool's projections answer as a pool that does not exist to a caller who cannot read it
    Given token "h" sends "POST /api/v1/worker-pools" with '{"name":"Closed","space":"{team}","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "closed"
    And the member key is space-reader on the space "legacy"
    When token "m" sends "GET /api/v1/worker-pools/{closed}/search-users?input=%7B%7D" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/worker-pools/{closed}/search-users-suggestions?input=%7B%7D" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/worker-pools/{closed}/search-schedulable-runs?input=%7B%7D" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/worker-pools/{closed}/search-schedulable-runs-suggestions?input=%7B%7D" through A
    Then it answers a 404 problem "not-found"

  @op:publicWorkerPoolSearchUsers @op:publicWorkerPoolSearchUsersSuggestions @op:publicWorkerPoolSearchSchedulableRuns @op:publicWorkerPoolSearchSchedulableRunsSuggestions @op:workerPoolSearchUsers @op:workerPoolSearchSchedulableRuns @op:workerPoolSearchWorkers @op:workerPoolCycle @op:workerPoolReset @op:workerPool @op:workerUsage @case:failure
  Scenario: A stale cursor, a filter on an ordering field, a cycle out of reach and the public selector on a private route are refused
    Given token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    When token "h" sends "GET /api/v1/worker-pools/public/search-users?input=%7B%22after%22%3A%22nope%22%7D" through B
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "GET /api/v1/worker-pools/public/search-users-suggestions?input=%7B%22predicates%22%3A%5B%7B%22field%22%3A%22colour%22%2C%22constraint%22%3A%7B%22stringMatches%22%3A%5B%22x%22%5D%7D%7D%5D%7D" through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "GET /api/v1/worker-pools/public/search-schedulable-runs?input=%7B%22after%22%3A%22nope%22%7D" through B
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "GET /api/v1/worker-pools/public/search-schedulable-runs-suggestions?input=%7B%22predicates%22%3A%5B%7B%22field%22%3A%22position%22%2C%22constraint%22%3A%7B%22stringMatches%22%3A%5B%221%22%5D%7D%7D%5D%7D" through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "GET /api/v1/worker-pools/{pool}/search-users?input=%7B%22after%22%3A%22nope%22%7D" through B
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "GET /api/v1/worker-pools/{pool}/search-schedulable-runs?input=%7B%22after%22%3A%22nope%22%7D" through A
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "GET /api/v1/worker-pools/nope/search-users?input=%7B%7D" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/worker-usage?input=%7B%22cyclesAgo%22%3A6%7D" through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "#/query/input" as "rule"
    When token "h" sends "GET /api/v1/worker-pools/public" through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "#/path/workerPoolId" as "rule"
    When token "h" sends "GET /api/v1/worker-pools/public/search-workers?input=%7B%7D" through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "POST /api/v1/worker-pools/public/cycle" through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "POST /api/v1/worker-pools/public/reset" with '{"certificateSigningRequest":"{csr}"}' through A
    Then it answers a 422 problem "validation-failed"

  @op:searchWorkerPoolsSuggestions @op:workerPoolSearchUsersSuggestions @op:workerPoolSearchSchedulableRunsSuggestions @case:failure
  Scenario: A private pool's suggestions refuse a predicate on a field their search does not filter
    Given token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    When token "h" sends "POST /api/v1/worker-pools/search/suggestions" with '{"fields":["label"],"predicates":[{"field":"colour","constraint":{"stringMatches":["x"]}}]}' through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "GET /api/v1/worker-pools/{pool}/search-users-suggestions?input=%7B%22predicates%22%3A%5B%7B%22field%22%3A%22colour%22%2C%22constraint%22%3A%7B%22stringMatches%22%3A%5B%22x%22%5D%7D%7D%5D%7D" through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "GET /api/v1/worker-pools/{pool}/search-schedulable-runs-suggestions?input=%7B%22predicates%22%3A%5B%7B%22field%22%3A%22position%22%2C%22constraint%22%3A%7B%22stringMatches%22%3A%5B%221%22%5D%7D%7D%5D%7D" through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "GET /api/v1/worker-pools/nope/search-users-suggestions?input=%7B%7D" through A
    Then it answers a 404 problem "not-found"
