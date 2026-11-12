@postgres
Feature: Private worker pools
  An account keeps private worker pools in its spaces. A pool is created
  from a certificate signing request for an EC P-256 key, whose private
  half never leaves the customer, and answers its pool token: the server's
  URL, the pool and the certificate the installation's pool CA signs for
  the key. A reset signs a new certificate under a new serial, which
  leaves registrations under the old one behind. A pool in a space the
  caller cannot read, or of another account, answers as one that does not
  exist. Private pools need the private-worker-pools capability, which
  Starter+ and above hold. The home key is space-admin on the account's
  top space; the member key holds only the bindings a scenario gives it.
  Team inherits from acme.
  ref: decision 0008

  Background:
    Given two infraweave servers, A and B, on one database, with an operator key
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through B for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And a certificate signing request for a P-256 key is kept as "csr"
    And a certificate signing request for a P-256 key is kept as "csr2"

  @op:workerPoolCreate @op:workerPool @op:workerPools @op:workerPoolUpdate @op:workerPoolPatch @op:workerPoolReset @op:workerPoolDelete @case:success
  Scenario: An account admin creates a pool from a P-256 key, reads, changes, resets and deletes it
    When token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"{team}","labels":["b","a"],"description":"Our builders","certificateSigningRequest":"{csr}"}' through A
    Then it answers 201
    And the answer's id names "pool"
    And the answer's name is '"Builders"'
    And the answer's space is '"{team}"'
    And the answer's labels is '["b","a"]'
    And the answer's preparingTimeoutSeconds is '900'
    And the answer's workersCount is '0'
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "first"
    When token "h" sends "GET /api/v1/worker-pools/{pool}" through B
    Then it answers 200
    And the answer's description is '"Our builders"'
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "same"
    When token "h" sends "GET /api/v1/worker-pools" through A
    Then the answer lists the worker pools "{pool}"
    When token "h" sends "PUT /api/v1/worker-pools/{pool}" with '{"name":"Runners","space":"acme","preparingTimeoutSeconds":5,"driftDetectionRunLimit":2}' through B
    Then it answers 200
    And the answer's name is '"Runners"'
    And the answer's space is '"acme"'
    And the answer's preparingTimeoutSeconds is '5'
    And the answer's driftDetectionRunLimit is '2'
    And the answer's labels is '[]'
    And the answer leaves out "description"
    When token "h" sends "PATCH /api/v1/worker-pools/{pool}" with '{"description":"patched","preparingTimeoutSeconds":null}' through A
    Then it answers 200
    And the answer's description is '"patched"'
    And the answer's name is '"Runners"'
    And the answer's preparingTimeoutSeconds is '900'
    And the answer's driftDetectionRunLimit is '2'
    When token "h" sends "POST /api/v1/worker-pools/{pool}/reset" with '{"certificateSigningRequest":"{csr2}"}' through B
    Then it answers 200
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "second"
    And the pool "pool" no longer has the serial kept as "first"
    When token "h" sends "DELETE /api/v1/worker-pools/{pool}" through A
    Then it answers 200
    And the answer's deleted is 'true'
    When token "h" sends "GET /api/v1/worker-pools/{pool}" through B
    Then it answers a 404 problem "not-found"

  @op:searchWorkerPools @op:searchWorkerPoolsSuggestions @op:workerPools @case:success
  Scenario: Searches find the pools in the spaces the caller reads
    Given token "h" sends "POST /api/v1/worker-pools" with '{"name":"Top","space":"acme","labels":["eu"],"certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "top"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Ours","space":"{team}","labels":["eu","prod"],"certificateSigningRequest":"{csr}"}' through B
    And the answer's id names "ours"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Other","space":"legacy","certificateSigningRequest":"{csr2}"}' through A
    And the answer's id names "other"
    When token "h" sends "POST /api/v1/worker-pools/search" with '{}' through B
    Then it answers 200
    And the answer lists the worker pools "{other}, {ours}, {top}"
    When token "h" sends "POST /api/v1/worker-pools/search" with '{"predicates":[{"field":"label","constraint":{"stringMatches":["prod"]}}]}' through A
    Then the answer lists the worker pools "{ours}"
    When token "h" sends "POST /api/v1/worker-pools/search/suggestions" with '{"fields":["label"]}' through B
    Then it answers 200
    And the answer suggests the label values "eu:2, prod:1" of 3 worker pools
    When token "h" sends "GET /api/v1/worker-pools?limit=2" through A
    Then the answer lists the worker pools "{other}, {ours}"

  @op:workerPools @op:workerPool @op:workerPoolCreate @op:workerPoolUpdate @op:workerPoolPatch @op:workerPoolReset @op:workerPoolDelete @op:searchWorkerPools @op:searchWorkerPoolsSuggestions @case:denial
  Scenario: A space reader reads the pools of its spaces alone and changes none, and another account sees none
    Given token "h" sends "POST /api/v1/worker-pools" with '{"name":"Open","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "open"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Closed","space":"{team}","certificateSigningRequest":"{csr}"}' through B
    And the answer's id names "closed"
    And the member key is space-reader on the space "legacy"
    When token "m" sends "GET /api/v1/worker-pools" through A
    Then the answer lists the worker pools "{open}"
    When token "m" sends "POST /api/v1/worker-pools/search" with '{}' through B
    Then the answer lists the worker pools "{open}"
    When token "m" sends "POST /api/v1/worker-pools/search/suggestions" with '{"fields":["label"]}' through A
    Then it answers 200
    When token "m" sends "GET /api/v1/worker-pools/{open}" through B
    Then it answers 200
    When token "m" sends "GET /api/v1/worker-pools/{closed}" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/worker-pools" with '{"name":"Mine","space":"legacy","certificateSigningRequest":"{csr}"}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"WORKER_POOL_CREATE"'
    When token "m" sends "PUT /api/v1/worker-pools/{open}" with '{"name":"Mine"}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"WORKER_POOL_UPDATE"'
    When token "m" sends "PATCH /api/v1/worker-pools/{open}" with '{"description":"mine"}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"WORKER_POOL_UPDATE"'
    When token "m" sends "POST /api/v1/worker-pools/{open}/reset" with '{"certificateSigningRequest":"{csr2}"}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"WORKER_POOL_RESET"'
    When token "m" sends "DELETE /api/v1/worker-pools/{open}" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"WORKER_POOL_DELETE"'
    When the sibling key is exchanged through A for token "s"
    And token "s" sends "GET /api/v1/worker-pools/{open}" through A
    Then it answers a 404 problem "not-found"
    When token "s" sends "GET /api/v1/worker-pools" through B
    Then the answer lists the worker pools ""

  @op:workerPoolUpdate @case:denial
  Scenario: Moving a pool needs WORKER_POOL_CREATE on the space it moves to
    Given token "h" sends "POST /api/v1/worker-pools" with '{"name":"Open","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "open"
    And the member key is space-admin on the space "legacy"
    And the member key is space-reader on the space "{team}"
    When token "m" sends "PUT /api/v1/worker-pools/{open}" with '{"name":"Open","space":"{team}"}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"WORKER_POOL_CREATE"'
    When token "m" sends "PUT /api/v1/worker-pools/{open}" with '{"name":"Open","space":"nowhere"}' through A
    Then it answers a 404 problem "not-found"

  @op:workerPoolCreate @op:workerPoolReset @op:workerPoolUpdate @op:workerPoolPatch @op:workerPool @op:workerPoolDelete @op:workerPools @op:searchWorkerPools @case:failure
  Scenario: A pool needs a P-256 key, a name of its own, a space and a positive preparing timeout
    Given a certificate signing request for a P-384 key is kept as "p384"
    And a certificate signing request for an RSA key is kept as "rsa"
    When token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","certificateSigningRequest":"{rsa}"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/certificateSigningRequest" as "rule"
    And the answer's detail is '"The certificate signing request is refused."'
    When token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","certificateSigningRequest":"{p384}"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/certificateSigningRequest" as "rule"
    When token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","certificateSigningRequest":"bm90IGEgcmVxdWVzdA=="}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "POST /api/v1/worker-pools" with '{"name":" ","certificateSigningRequest":"{csr}"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/name" as "required"
    When token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","preparingTimeoutSeconds":0,"certificateSigningRequest":"{csr}"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/preparingTimeoutSeconds" as "minimum"
    When token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"nowhere","certificateSigningRequest":"{csr}"}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","certificateSigningRequest":"{csr}"}' through A
    Then it answers 201
    And the answer's space is '"acme"'
    And the answer's id names "pool"
    When token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through B
    Then it answers a 409 problem "already-exists"
    When token "h" sends "POST /api/v1/worker-pools/{pool}/reset" with '{"certificateSigningRequest":"{rsa}"}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "PUT /api/v1/worker-pools/{pool}" with '{"name":"Builders","space":"nowhere"}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "PATCH /api/v1/worker-pools/{pool}" with '{"name":""}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "GET /api/v1/worker-pools/nope" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/worker-pools/nope/reset" with '{"certificateSigningRequest":"{csr}"}' through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/worker-pools?cursor=nope" through B
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "POST /api/v1/worker-pools/search" with '{"after":"nope"}' through A
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "DELETE /api/v1/worker-pools/{pool}" through B
    Then it answers 200
    When token "h" sends "DELETE /api/v1/worker-pools/{pool}" through A
    Then it answers a 404 problem "not-found"

  @op:workerPools @op:workerPoolCreate @op:searchWorkerPools @case:denial
  Scenario: An organization on Starter or Free is refused private worker pools, and one on Starter+ is not
    Given the operator key is exchanged through A for token "op"
    When token "op" gives the home organization the tier "Starter" through A
    And token "op" turns billing on through A
    Then "GET /api/v1/worker-pools" through B with token "h" eventually answers a 403 problem "feature-not-in-tier"
    And the problem names the feature "private-worker-pools"
    When token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","certificateSigningRequest":"{csr}"}' through A
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "POST /api/v1/worker-pools/search" with '{}' through B
    Then it answers a 403 problem "feature-not-in-tier"
    When token "op" gives the home organization the tier "Starter+" through A
    Then "GET /api/v1/worker-pools" through B with token "h" eventually answers 200
    When token "op" gives the home organization the tier "Free" through A
    Then "GET /api/v1/worker-pools" through A with token "h" eventually answers a 403 problem "feature-not-in-tier"
    And the problem names the feature "private-worker-pools"

  @op:stackCreate @op:stackUpdate @op:stack @op:stackAttachableWorkerPools @op:attachableWorkerPoolsInSpace @op:workerPoolDelete @op:workerPoolUpdate @case:success
  Scenario: A stack names a pool its space reaches, and the pool is neither deleted nor moved out of reach while it does
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Solo","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    And the answer's id is the space "solo"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"A Top","space":"acme","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "top"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"B Team","space":"{team}","certificateSigningRequest":"{csr}"}' through B
    And the answer's id names "teampool"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"C Solo","space":"{solo}","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "solopool"
    When token "h" sends "GET /api/v1/attachable-worker-pools-in-space?id={team}" through B
    Then the answer lists the worker pools "{top}, {teampool}"
    When token "h" sends "GET /api/v1/attachable-worker-pools-in-space?id={solo}" through A
    Then the answer lists the worker pools "{solopool}"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"{team}","workerPool":"{teampool}"},"manageState":false}' through A
    Then it answers 201
    And the answer's workerPool is '{"id":"{teampool}"}'
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then the answer's workerPool is '{"id":"{teampool}"}'
    When token "h" sends "GET /api/v1/stacks/infra/attachable-worker-pools" through A
    Then the answer lists the worker pools "{top}, {teampool}"
    When token "h" sends "PATCH /api/v1/stacks/infra" with '{"description":"patched"}' through B
    Then it answers 200
    And the answer's workerPool is '{"id":"{teampool}"}'
    When token "h" sends "DELETE /api/v1/worker-pools/{teampool}" through A
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "PUT /api/v1/worker-pools/{teampool}" with '{"name":"B Team","space":"{solo}"}' through B
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "PUT /api/v1/worker-pools/{teampool}" with '{"name":"B Team","space":"acme"}' through A
    Then it answers 200
    When token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"main","space":"{team}"}' through B
    Then it answers 200
    And the answer leaves out "workerPool"
    When token "h" sends "DELETE /api/v1/worker-pools/{teampool}" through A
    Then it answers 200

  @op:stackCreate @op:stackUpdate @op:stackAttachableWorkerPools @op:attachableWorkerPoolsInSpace @case:failure
  Scenario: A stack cannot name a pool its space does not reach, nor a pool that does not exist
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Solo","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    And the answer's id is the space "solo"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Solo","space":"{solo}","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "solopool"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"{team}","workerPool":"{solopool}"},"manageState":false}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/input/workerPool" as "not-found"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"{team}","workerPool":"nope"},"manageState":false}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/input/workerPool" as "not-found"
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"{team}"},"manageState":false}' through B
    When token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"main","space":"{team}","workerPool":"{solopool}"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/workerPool" as "not-found"
    When token "h" sends "GET /api/v1/stacks/nope/attachable-worker-pools" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/attachable-worker-pools-in-space?id=nowhere" through A
    Then it answers a 404 problem "not-found"

  @op:stackAttachableWorkerPools @op:attachableWorkerPoolsInSpace @case:denial
  Scenario: A space reader lists only the attachable pools of the spaces it reads, and none of a space it cannot read
    Given token "h" sends "POST /api/v1/worker-pools" with '{"name":"Team","space":"{team}","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "teampool"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Legacy","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"{team}"},"manageState":false}' through B
    And the member key is space-reader on the space "{team}"
    When token "m" sends "GET /api/v1/attachable-worker-pools-in-space?id={team}" through B
    Then the answer lists the worker pools "{teampool}"
    When token "m" sends "GET /api/v1/stacks/infra/attachable-worker-pools" through A
    Then the answer lists the worker pools "{teampool}"
    When token "m" sends "GET /api/v1/attachable-worker-pools-in-space?id=legacy" through B
    Then it answers a 404 problem "not-found"

  @op:accountUpdateDefaultWorkerPoolRunnerImages @op:defaultPrivateWorkerPoolRunnerImage @case:success
  Scenario: An account admin sets the default runner images, which the default private runner image then answers
    When token "h" sends "GET /api/v1/default-private-worker-pool-runner-image" through A
    Then the answer is '"ghcr.io/weaveplatform/infraweave-runner-terraform"'
    When token "h" sends "POST /api/v1/account/update-default-worker-pool-runner-images" with '{"privateWorkerPoolRunnerImage":" ghcr.io/acme/runner:1 ","publicWorkerPoolRunnerImage":""}' through B
    Then it answers 200
    And the answer's privateWorkerPoolRunnerImage is '"ghcr.io/acme/runner:1"'
    And the answer leaves out "publicWorkerPoolRunnerImage"
    When token "h" sends "GET /api/v1/default-private-worker-pool-runner-image" through A
    Then the answer is '"ghcr.io/acme/runner:1"'
    When token "h" sends "POST /api/v1/account/update-default-worker-pool-runner-images" with '{}' through B
    Then it answers 200
    When token "h" sends "GET /api/v1/default-private-worker-pool-runner-image" through A
    Then the answer is '"ghcr.io/weaveplatform/infraweave-runner-terraform"'

  @op:accountUpdateDefaultWorkerPoolRunnerImages @case:denial
  Scenario: Only an account admin sets the default runner images
    Given the member key is space-admin on the space "{team}"
    When token "m" sends "POST /api/v1/account/update-default-worker-pool-runner-images" with '{"privateWorkerPoolRunnerImage":"ghcr.io/acme/runner:1"}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"ACCOUNT_ADMIN"'

  @op:accountUpdateDefaultWorkerPoolRunnerImages @case:failure
  Scenario: A default runner image with a space, too long, or an unknown field is refused
    When token "h" sends "POST /api/v1/account/update-default-worker-pool-runner-images" with '{"privateWorkerPoolRunnerImage":"two words"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/privateWorkerPoolRunnerImage" as "rule"
    When token "h" sends "POST /api/v1/account/update-default-worker-pool-runner-images" with '{"privateWorkerPoolRunnerImage":"ghcr.io/acme/runner","colour":"red"}' through B
    Then it answers a 400 problem "invalid-request"

  @op:workerDrainSet @op:workerPoolCycle @op:workerPoolSearchWorkers @op:workerPoolSearchWorkersSuggestions @op:workerPool @case:success
  Scenario: A launcher registers and polls, an account admin drains, finds and cycles it, and it deregisters
    Given token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"{team}","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "first"
    When the launcher "l" registers with the key of "csr" and the certificate "first" through A
    Then it answers 200
    When token "h" sends "GET /api/v1/worker-pools/{pool}" through B
    Then the answer's workersCount is '1'
    And the answer's busyWorkers is '0'
    When token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then the answer is '{}'
    When the launcher "l" holds a poll with '{"busy":true,"wait":60}' through A
    And token "h" sends "PUT /api/v1/worker-pools/{pool}/workers/{l}/drain" with '{"drain":true}' through B
    Then it answers 200
    And the answer's status is '"DRAINED"'
    And the held poll of "l" answers within 10 seconds
    And the answer is '{"drained":true}'
    When token "h" sends "GET /api/v1/worker-pools/{pool}/search-workers?input=%7B%22predicates%22%3A%5B%7B%22field%22%3A%22status%22%2C%22constraint%22%3A%7B%22enumEquals%22%3A%5B%22DRAINED%22%5D%7D%7D%5D%7D" through A
    Then the answer lists the workers "l"
    When token "h" sends "GET /api/v1/worker-pools/{pool}/search-workers?input=%7B%22predicates%22%3A%5B%7B%22field%22%3A%22status%22%2C%22constraint%22%3A%7B%22enumEquals%22%3A%5B%22IDLE%22%5D%7D%7D%5D%7D" through B
    Then the answer lists the workers ""
    When token "h" sends "GET /api/v1/worker-pools/{pool}/search-workers-suggestions?input=%7B%22fields%22%3A%5B%22status%22%5D%7D" through A
    Then it answers 200
    When the launcher "l" holds a poll with '{"busy":true,"drained":true,"wait":60}' through B
    And token "h" sends "POST /api/v1/worker-pools/{pool}/cycle" through A
    Then it answers 200
    And the held poll of "l" answers within 10 seconds
    And the answer is '{"drained":true,"signals":["cycle"]}'
    When token "l" sends "POST /worker/v1/deregister" through A
    Then it answers 204
    When token "l" sends "POST /worker/v1/poll" with '{"busy":false}' through B
    Then it answers a 401 problem "unauthenticated"
    When token "h" sends "GET /api/v1/worker-pools/{pool}" through A
    Then the answer's workersCount is '0'

  @op:workerDrainSet @op:workerPoolCycle @op:workerPoolSearchWorkers @op:workerPoolSearchWorkersSuggestions @case:denial
  Scenario: The worker API refuses a user token, a replayed registration and a replaced certificate, and a registration beyond private-workers
    Given the operator key is exchanged through A for token "op"
    And token "op" gives the home organization the tier "Starter+" through A
    And token "op" turns billing on through A
    And "GET /api/v1/worker-pools" through B with token "h" eventually answers 200
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"{team}","certificateSigningRequest":"{csr}"}' through B
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "first"
    When token "h" sends "POST /worker/v1/poll" with '{"busy":false}' through B
    Then it answers a 401 problem "unauthenticated"
    When the launcher "l" registers with the key of "csr" and the certificate "first" through B
    Then it answers 200
    When the launcher "l" sends its registration again through B
    Then it answers a 401 problem "unauthenticated"
    When the launcher "l2" registers with the key of "csr" and the certificate "first" through B
    Then it answers 200
    When the launcher "l3" registers with the key of "csr" and the certificate "first" through B
    Then it answers a 403 problem "limit-reached"
    And the problem names the limit "private-workers" of the organization at 2
    When the launcher "l" registers with the key of "csr" and the certificate "first" as the worker "l" through B
    Then it answers 200
    And the answer's worker is '"{l}"'
    When token "m" sends "PUT /api/v1/worker-pools/{pool}/workers/{l}/drain" with '{"drain":true}' through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/worker-pools/{pool}/cycle" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/worker-pools/{pool}/search-workers?input=%7B%7D" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/worker-pools/{pool}/search-workers-suggestions?input=%7B%7D" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/worker-pools/{pool}/reset" with '{"certificateSigningRequest":"{csr2}"}' through B
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "second"
    And token "l" sends "POST /worker/v1/poll" with '{"busy":false}' through B
    Then it answers a 401 problem "unauthenticated"
    When the launcher "old" registers with the key of "csr" and the certificate "first" through B
    Then it answers a 401 problem "unauthenticated"
    When the launcher "l" registers with the key of "csr2" and the certificate "second" as the worker "l" through B
    Then it answers 200
    And the answer's worker is '"{l}"'

  @op:workerDrainSet @op:workerPoolCycle @op:workerPoolSearchWorkers @op:workerPoolSearchWorkersSuggestions @case:failure
  Scenario: A drain of a worker the pool lacks, a cycle of no pool, a bad cursor and a malformed registration are refused
    Given token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    When token "h" sends "PUT /api/v1/worker-pools/{pool}/workers/nope/drain" with '{"drain":true}' through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "PUT /api/v1/worker-pools/{pool}/workers/01KACME000000000000000000A/drain" with '{"drain":true,"colour":"red"}' through B
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "POST /api/v1/worker-pools/nope/cycle" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/worker-pools/{pool}/search-workers?input=%7B%22after%22%3A%22nope%22%7D" through B
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "GET /api/v1/worker-pools/nope/search-workers-suggestions?input=%7B%7D" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /worker/v1/register" with '{"registration":"garbage"}' through A
    Then it answers a 401 problem "unauthenticated"
