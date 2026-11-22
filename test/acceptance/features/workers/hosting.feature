@postgres
Feature: Controller-hosted worker pools
  A Kubernetes worker controller hosts a pool for one resource, its owner:
  the cluster ID, namespace and resource UID. It binds the pool to that
  owner with a proof the pool's key signs, so a pool bound to one owner
  admits no other owner's binding or registration. Each registration of a
  bound pool names its owner and a slot, and a slot holds one worker:
  registering it again resumes that worker. A binding is released only
  while no worker remains. A managed pool is created bound to its owner,
  under an Idempotency-Key, and found again by that owner. The home key is
  space-admin on the account's top space; the member key holds only the
  bindings a scenario gives it. Team inherits from acme.

  Background:
    Given two infraweave servers, A and B, on one database, with an operator key
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through B for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And a certificate signing request for a P-256 key is kept as "csr"
    And a certificate signing request for a P-256 key is kept as "csr2"

  @op:workerPoolBind @op:workerPoolRelease @op:workerRegister @op:workerDeregister @case:success
  Scenario: An owner binds a pool, resumes its slots and releases it once no worker remains
    Given token "h" sends "POST /api/v1/worker-pools" with '{"name":"Hosted","space":"{team}","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "first"
    When the owner "k" binds the pool of the certificate "first" with the key of "csr" through A
    Then it answers 204
    When the owner "k" binds the pool of the certificate "first" with the key of "csr" through B
    Then it answers 204
    When the launcher "s0" registers as slot 0 of the owner "k" with the key of "csr" and the certificate "first" through A
    Then it answers 200
    When the launcher "again" registers as slot 0 of the owner "k" with the key of "csr" and the certificate "first" through B
    Then it answers 200
    And the answer's worker is '"{s0}"'
    When the launcher "s1" registers as slot 1 of the owner "k" with the key of "csr" and the certificate "first" through B
    Then it answers 200
    When token "h" sends "GET /api/v1/worker-pools/{pool}" through A
    Then the answer's workersCount is '2'
    When the owner "k" releases the pool of the certificate "first" with the key of "csr" through A
    Then it answers a 409 problem "invalid-state"
    When token "again" sends "POST /worker/v1/deregister" through A
    Then it answers 204
    And token "s1" sends "POST /worker/v1/deregister" through B
    And it answers 204
    When the owner "k" releases the pool of the certificate "first" with the key of "csr" through B
    Then it answers 204
    When the owner "other" binds the pool of the certificate "first" with the key of "csr" through A
    Then it answers 204

  @op:workerPoolBind @op:workerPoolRelease @op:workerRegister @case:denial
  Scenario: A bound pool refuses another owner, an ownerless registration and an account token
    Given token "h" sends "POST /api/v1/worker-pools" with '{"name":"Hosted","space":"{team}","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "first"
    And the owner "k" binds the pool of the certificate "first" with the key of "csr" through A
    And it answers 204
    When the owner "thief" binds the pool of the certificate "first" with the key of "csr" through B
    Then it answers a 409 problem "invalid-state"
    When the owner "thief" releases the pool of the certificate "first" with the key of "csr" through B
    Then it answers a 409 problem "invalid-state"
    When the launcher "t" registers as slot 0 of the owner "thief" with the key of "csr" and the certificate "first" through A
    Then it answers a 409 problem "invalid-state"
    When the launcher "plain" registers with the key of "csr" and the certificate "first" through A
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "POST /worker/v1/pools/bind" with '{"proof":"{h}"}' through B
    Then it answers a 401 problem "unauthenticated"
    When token "h" sends "POST /worker/v1/poll" with '{"busy":false}' through B
    Then it answers a 401 problem "unauthenticated"

  @op:workerPoolBind @op:workerPoolRelease @op:workerRegister @case:failure
  Scenario: Malformed proofs, a slot without an owner and a replaced certificate are refused
    Given token "h" sends "POST /api/v1/worker-pools" with '{"name":"Hosted","space":"{team}","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "first"
    And token "h" sends "GET /api/v1/worker-pools/{pool}" through A
    And the answer's ETag is kept as "pool"
    When the owner "k" binds with a malformed proof through A
    Then it answers a 401 problem "unauthenticated"
    When the owner "k" releases with a malformed proof through B
    Then it answers a 401 problem "unauthenticated"
    When the launcher "s" registers as slot 0 without an owner with the key of "csr" and the certificate "first" through A
    Then it answers a 422 problem "validation-failed"
    Given the owner "k" binds the pool of the certificate "first" with the key of "csr" through A
    And token "h" sends "POST /api/v1/worker-pools/{pool}/reset" with '{"certificateSigningRequest":"{csr2}","releaseBinding":true}' if matching the ETag "pool" through B
    And it answers 200
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "second"
    When the owner "k" binds the pool of the certificate "first" with the key of "csr" through A
    Then it answers a 401 problem "unauthenticated"
    When the owner "other" binds the pool of the certificate "second" with the key of "csr2" through B
    Then it answers 204

  @op:workerPoolCreate @op:workerPoolManaged @op:workerPoolBind @op:workerRegister @case:success
  Scenario: A managed pool is created once for its owner, found by it and bound to it
    When token "h" sends "POST /api/v1/worker-pools" with '{"name":"Managed","space":"{team}","certificateSigningRequest":"{csr}","management":{"clusterID":"acceptance","namespace":"workers","resourceUID":"k"}}' and the Idempotency-Key "worker-pool-k" through A
    Then it answers 201
    And the answer's id names "pool"
    And the answer's managedByK8sController is 'true'
    And the answer's management is '{"clusterID":"acceptance","namespace":"workers","resourceUID":"k"}'
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "first"
    When token "h" sends "POST /api/v1/worker-pools" with '{"name":"Managed","space":"{team}","certificateSigningRequest":"{csr}","management":{"clusterID":"acceptance","namespace":"workers","resourceUID":"k"}}' and the Idempotency-Key "worker-pool-k" through B
    Then it answers 201
    And the answer's id is '"{pool}"'
    When token "h" sends "GET /api/v1/worker-pools/managed?clusterID=acceptance&namespace=workers&resourceUID=k" through B
    Then it answers 200
    And the answer's id is '"{pool}"'
    When the launcher "s0" registers as slot 0 of the owner "k" with the key of "csr" and the certificate "first" through A
    Then it answers 200
    When the owner "other" binds the pool of the certificate "first" with the key of "csr" through B
    Then it answers a 409 problem "invalid-state"

  @op:workerPoolCreate @op:workerPoolManaged @case:denial
  Scenario: A managed pool is hidden from a caller who cannot read its space
    Given token "h" sends "POST /api/v1/worker-pools" with '{"name":"Managed","space":"{team}","certificateSigningRequest":"{csr}","management":{"clusterID":"acceptance","namespace":"workers","resourceUID":"k"}}' and the Idempotency-Key "worker-pool-k" through A
    And it answers 201
    When token "m" sends "GET /api/v1/worker-pools/managed?clusterID=acceptance&namespace=workers&resourceUID=k" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/worker-pools" with '{"name":"Other","space":"{team}","certificateSigningRequest":"{csr2}","management":{"clusterID":"acceptance","namespace":"workers","resourceUID":"m"}}' and the Idempotency-Key "worker-pool-m" through B
    Then it answers a 404 problem "not-found"

  @op:workerPoolCreate @op:workerPoolManaged @case:failure
  Scenario: A managed pool needs an Idempotency-Key and a complete owner
    When token "h" sends "POST /api/v1/worker-pools" with '{"name":"Managed","space":"{team}","certificateSigningRequest":"{csr}","management":{"clusterID":"acceptance","namespace":"workers","resourceUID":"k"}}' and the Idempotency-Key " " through A
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "GET /api/v1/worker-pools/managed?clusterID=acceptance&namespace=workers&resourceUID=absent" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/worker-pools/managed?clusterID=Not_A_Label&namespace=workers&resourceUID=k" through B
    Then it answers a 422 problem "validation-failed"

  @op:workerRegister @case:denial
  Scenario: Slots of every pool share the organization's private-workers limit
    Given the operator key is exchanged through A for token "op"
    And token "op" gives the home organization the tier "Business" through A
    And token "op" turns billing on through A
    And "GET /api/v1/worker-pools" through B with token "h" eventually answers 200
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"One","space":"{team}","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "one"
    And the answer's pool token names the pool "one" and chains to the pool CA, its serial kept as "one"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Two","space":"{team}","certificateSigningRequest":"{csr2}"}' through B
    And the answer's id names "two"
    And the answer's pool token names the pool "two" and chains to the pool CA, its serial kept as "two"
    And the owner "k1" binds the pool of the certificate "one" with the key of "csr" through A
    And the owner "k2" binds the pool of the certificate "two" with the key of "csr2" through B
    When the launcher "a0" registers as slot 0 of the owner "k1" with the key of "csr" and the certificate "one" through A
    And the launcher "a1" registers as slot 1 of the owner "k1" with the key of "csr" and the certificate "one" through B
    And the launcher "b0" registers as slot 0 of the owner "k2" with the key of "csr2" and the certificate "two" through A
    Then it answers 200
    When the launcher "b1" registers as slot 1 of the owner "k2" with the key of "csr2" and the certificate "two" through B
    Then it answers a 403 problem "limit-reached"
    And the problem names the limit "private-workers" of the organization at 3
    When the launcher "a0again" registers as slot 0 of the owner "k1" with the key of "csr" and the certificate "one" through B
    Then it answers 200
    And the answer's worker is '"{a0}"'
