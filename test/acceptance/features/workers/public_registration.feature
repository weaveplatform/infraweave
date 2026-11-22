@postgres
Feature: Installation public worker credentials remain fenced
  Public registrations use the installation pool certificate and explicit
  image-policy capability. Session credentials never authorize account or
  operator routes, and replaced certificates cannot regain a worker session.

  Background:
    Given two infraweave servers, A and B, on one database, with an operator key and the settings '{"worker":{"public_pool":{"enabled":true}}}'
    And the operator key is exchanged through A for token "op"
    And the home key is exchanged through B for token "h"
    And a certificate signing request for a P-256 key is kept as "csr"
    And a certificate signing request for a P-256 key is kept as "csr2"
    And token "op" sends "POST /operator/v1/public-worker-pool" with '{"certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "public"
    And the answer's ETag is kept as "first"
    And the answer's pool token names the pool "public" and chains to the pool CA, its serial kept as "first"

  @op:workerRegister @op:workerPoll @case:success
  Scenario: Public registration resumes the same worker without consuming private capacity
    When the public launcher "l" registers with the key of "csr" and the certificate "first" through B
    Then it answers 200
    When the public launcher "resumed" registers with the key of "csr" and the certificate "first" as the worker "l" through A
    Then it answers 200
    And the answer's worker is '"{l}"'
    When token "resumed" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then it answers 200
    And the answer has no worker assignment

  @op:workerRegister @op:workerPoll @case:denial
  Scenario: Replays missing capabilities and wrong credential audiences are refused
    When the public launcher "expired" registers an expired proof with the key of "csr" and the certificate "first" through A
    Then it answers a 401 problem "unauthenticated"
    When the launcher "old" registers with the key of "csr" and the certificate "first" through B
    Then it answers a 422 problem "validation-failed"
    When the public launcher "l" registers with the key of "csr" and the certificate "first" through B
    Then it answers 200
    When the launcher "l" sends its registration again through A
    Then it answers a 401 problem "unauthenticated"
    When token "l" sends "GET /operator/v1/public-worker-pool" through A
    Then it answers a 401 problem "unauthenticated"
    When token "l" sends "GET /api/v1/stacks" through B
    Then it answers a 401 problem "unauthenticated"
    When token "h" sends "POST /worker/v1/poll" with '{"busy":false}' through A
    Then it answers a 401 problem "unauthenticated"
    When token "op" sends "POST /worker/v1/poll" with '{"busy":false}' through A
    Then it answers a 401 problem "unauthenticated"

  @op:publicWorkerPoolReset @op:workerRegister @op:workerPoll @case:denial
  Scenario: Rotation revokes old sessions even after the replacement key registers
    Given the public launcher "l" registers with the key of "csr" and the certificate "first" through B
    And it answers 200
    When token "op" sends "POST /operator/v1/public-worker-pool/reset" with '{"certificateSigningRequest":"{csr2}","releaseBinding":true}' if matching the ETag "first" through A
    Then it answers a 409 problem "invalid-state"
    When token "op" sends "POST /operator/v1/public-worker-pool/reset" with '{"certificateSigningRequest":"{csr2}"}' if matching the ETag "first" through A
    Then it answers 200
    And the answer's pool token names the pool "public" and chains to the pool CA, its serial kept as "second"
    When token "l" sends "POST /worker/v1/poll" with '{"busy":false}' through B
    Then it answers a 401 problem "unauthenticated"
    When the public launcher "old" registers with the key of "csr" and the certificate "first" through B
    Then it answers a 401 problem "unauthenticated"
    When the public launcher "replacement" registers with the key of "csr2" and the certificate "second" as the worker "l" through A
    Then it answers 200
    When token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then it answers a 401 problem "unauthenticated"
