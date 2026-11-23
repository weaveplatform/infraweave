@postgres
Feature: Private certificate reset revokes old worker sessions
  @op:workerRegister @op:workerPoll @op:workerDeregister @op:workerPoolReset @case:denial
  Scenario: An old session stays revoked after a replacement certificate resumes the same worker
    Given two infraweave servers, A and B, on one database, with an operator key and worker sessions lasting "10m"
    And the home key is exchanged through A for token "h"
    And the operator key is exchanged through A for token "op"
    And token "op" gives the home organization the tier "Business" through A
    And token "op" turns billing on through A
    And "GET /api/v1/worker-pools" through B with token "h" eventually answers 200
    And a certificate signing request for a P-256 key is kept as "csr"
    And a certificate signing request for a P-256 key is kept as "csr2"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","certificateSigningRequest":"{csr}"}' through A
    And it answers 201
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "first"
    When the launcher "original" registers with the key of "csr" and the certificate "first" through A
    Then it answers 200
    And worker token "original" carries certificate generation "first"
    And worker "original" liveness is kept as "beforeReset"
    When token "h" sends "POST /api/v1/worker-pools/{pool}/reset" with '{"certificateSigningRequest":"{csr2}"}' through B
    Then it answers 200
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "second"
    When token "original" sends "POST /worker/v1/poll" with '{"busy":true,"wait":5}' through A
    Then it answers a 401 problem "unauthenticated"
    And worker "original" liveness matches "beforeReset"
    When the launcher "replacement" registers with the key of "csr2" and the certificate "second" as the worker "original" through B
    Then it answers 200
    And the answer's worker is '"{original}"'
    When token "original" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through A
    Then it answers a 401 problem "unauthenticated"

    And worker token "replacement" carries certificate generation "second"
    And worker "replacement" liveness is kept as "replacementState"
    When token "original" sends "POST /worker/v1/poll" with '{"busy":true,"wait":5}' through B
    Then it answers a 401 problem "unauthenticated"
    And worker "replacement" liveness matches "replacementState"
    When token "original" sends "POST /worker/v1/deregister" through A
    Then it answers a 401 problem "unauthenticated"
    And worker "replacement" liveness matches "replacementState"
    When token "replacement" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then it answers 200
    And the renewed worker session is kept as "renewed" for worker "replacement"
    And worker token "renewed" carries certificate generation "second"
    When token "replacement" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through A
    Then it answers 200
    And the renewed worker session is kept as "retried" for worker "replacement"
    And worker token "retried" carries certificate generation "second"
    When the launcher "restarted" registers with the key of "csr2" and the certificate "second" as the worker "replacement" through A
    Then it answers 200
    And the answer's worker is '"{replacement}"'
    And worker token "restarted" carries certificate generation "second"
    When token "h" sends "GET /api/v1/worker-pools/{pool}" through B
    Then the answer's workersCount is '1'
    When token "renewed" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then it answers 200
    When token "restarted" sends "POST /worker/v1/deregister" through B
    Then it answers 204
    When token "renewed" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through A
    Then it answers a 401 problem "unauthenticated"
