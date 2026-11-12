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
