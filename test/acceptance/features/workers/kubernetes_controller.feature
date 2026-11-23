@kubernetes-lab @kubernetes @postgres
Feature: Kubernetes worker controller
  Real worker controllers, as separate processes, reconcile WorkerPool
  resources in the owned cluster against two infraweave servers on one
  database, which an HTTPS front balances. One controller leads its Lease;
  the others stand by. A Managed pool is created once for its resource,
  an Existing pool is bound to it, and each slot of its capacity holds one
  registered worker that a replacement controller resumes, and whose
  launcher polls for runs to execute in Pods, so ExecutorAvailable is
  True. The home key is
  space-admin on the account's top space; the member key holds only the
  bindings a scenario gives it.

  Background:
    Given the owned Terraform Kubernetes API and namespace are ready
    And the canonical worker CRDs are established in the owned cluster
    And two infraweave servers, A and B, on one database, with an operator key
    And the home key is exchanged through A for token "h"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And a controller namespace "home" holds the home management key

  @op:workerPoolCreate @op:workerPoolManaged @op:workerPoolBind @op:workerRegister @op:workerPoolDelete @case:success
  Scenario: Replicated controllers converge separate managed pools and resume their workers after the leader is replaced
    Given 2 worker controllers watch the controller namespaces
    When the Managed WorkerPool "build" of capacity 2 in the space "team" is applied in "home"
    And the Managed WorkerPool "small" of capacity 1 in the space "team" is applied in "home"
    Then the WorkerPool "build" eventually has 2 registered slots, its pool kept as "build"
    And the WorkerPool "small" eventually has 1 registered slots, its pool kept as "small"
    And the WorkerPool "build" eventually reports Bound True "Bound"
    And the WorkerPool "build" eventually reports ExecutorAvailable True "LauncherRunning"
    And the WorkerPools "build" and "small" hold distinct pools and tokens
    When token "h" sends "GET /api/v1/worker-pools/{build}" through A
    Then the answer's workersCount is '2'
    And the answer's managedByK8sController is 'true'
    When token "h" sends "GET /api/v1/worker-pools" through B
    Then it answers 200
    When the leading worker controller is replaced
    Then the WorkerPool "build" eventually has 2 registered slots, its pool kept as "same"
    And the workers of the WorkerPool "build" are the ones kept
    When token "h" sends "GET /api/v1/worker-pools/{same}" through B
    Then the answer's id is '"{build}"'
    And the answer's workersCount is '2'
    When the WorkerPool "build" is deleted
    Then the WorkerPool "build" is eventually gone
    When token "h" sends "GET /api/v1/worker-pools/{build}" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/worker-pools/{small}" through B
    Then the answer's workersCount is '1'

  @op:workerPoolBind @op:workerPoolRelease @op:workerRegister @case:success
  Scenario: An existing pool is bound to its resource and released when the resource is deleted
    Given a certificate signing request for a P-256 key is kept as "csr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Existing","space":"{team}","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "first"
    And 1 worker controller watch the controller namespaces
    When the Existing WorkerPool "existing" of capacity 2 over the pool of the certificate "first" with the key of "csr" in the space "team" is applied in "home"
    Then the WorkerPool "existing" eventually has 2 registered slots, its pool kept as "bound"
    When the owner "thief" binds the pool of the certificate "first" with the key of "csr" through B
    Then it answers a 409 problem "invalid-state"
    When the WorkerPool "existing" is deleted
    Then the WorkerPool "existing" is eventually gone
    When token "h" sends "GET /api/v1/worker-pools/{pool}" through A
    Then the answer's workersCount is '0'
    When the owner "next" binds the pool of the certificate "first" with the key of "csr" through B
    Then it answers 204

  @op:workerPoolCreate @op:workerPoolBind @case:denial
  Scenario: A controller cannot use another account, a space its key cannot write, or a pool another owner holds
    Given the member key is exchanged through B for token "m"
    And a controller namespace "member" holds the member management key
    And a certificate signing request for a P-256 key is kept as "csr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Taken","space":"{team}","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "taken"
    And the answer's pool token names the pool "taken" and chains to the pool CA, its serial kept as "takencert"
    And the owner "other" binds the pool of the certificate "takencert" with the key of "csr" through B
    And 1 worker controller watch the controller namespaces
    When the Managed WorkerPool "elsewhere" of capacity 1 in the space "team" is applied in "home" for another account
    And the Managed WorkerPool "unwritable" of capacity 1 in the space "team" is applied in "member"
    And the Existing WorkerPool "steal" of capacity 1 over the pool of the certificate "takencert" with the key of "csr" in the space "team" is applied in "home"
    Then the WorkerPool "elsewhere" eventually reports Bound False "AccountMismatch"
    And the WorkerPool "unwritable" eventually reports Bound False "Refused"
    And the WorkerPool "steal" eventually reports Bound False "OwnershipConflict"
    When token "h" sends "GET /api/v1/worker-pools/{taken}" through A
    Then the answer's workersCount is '0'

  @op:workerPoolCreate @op:workerRegister @op:workerPoolDelete @case:failure
  Scenario: The private-workers limit, broken credentials and a blocked deletion stay visible until they are resolved
    Given the operator key is exchanged through A for token "op"
    And token "op" gives the home organization the tier "Business" through A
    And token "op" turns billing on through A
    And "GET /api/v1/worker-pools" through B with token "h" eventually answers 200
    And 2 worker controllers watch the controller namespaces
    When the Managed WorkerPool "large" of capacity 5 in the space "team" is applied in "home"
    Then the WorkerPool "large" eventually has 3 registered slots, its pool kept as "large"
    And the WorkerPool "large" eventually reports Registered False "LimitReached"
    When the Managed WorkerPool "broken" of capacity 1 in the space "team" is applied in "home"
    And the credential Secret of "broken" holds a private key that is not PEM
    Then the WorkerPool "broken" eventually reports Bound False "CredentialsInvalid"
    When the management Secret of "home" is removed
    And the WorkerPool "large" is deleted
    Then the WorkerPool "large" eventually reports Terminating False "ManagementCredentialsMissing"
    When the management Secret of "home" holds the home management key
    Then the WorkerPool "large" is eventually gone
    When token "h" sends "GET /api/v1/worker-pools/{large}" through A
    Then it answers a 404 problem "not-found"
