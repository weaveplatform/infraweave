@postgres @vcs @docker
Feature: A launcher admits only the server-signed run token of its assignment
  Before a launcher uses an assignment's run token, it verifies the token
  against the run token keys of the server it is configured for, which
  its worker session reads from GET /worker/v1/run-token-keys: an ES256
  JWT whose kid names one of those public keys, whose signature that key
  verifies, whose iss is the server's, whose one aud is infraweave-runner,
  whose iw_kind is runner, whose sub is the assigned run, and whose times
  hold. A token that fails is refused before the run's specification,
  any initialization policy, its source, its tools or its container, and
  the run fails in PREPARING with a fixed note that names no token.
  The scenario's relay stands for the delivery path between an honest
  server and the launcher: it delivers a tampered token in place of the
  server's, or a genuine token of another run or another kind, and passes
  each later call that carries it on with the server's token, so only the
  launcher's own verification can refuse it. Each forged token alters a
  genuine one or is signed by a key the step makes: none is a reference
  capture. Stack infra runs in {runner.test-support}, which has no sh, so
  an admitted run fails INITIALIZING once its runner starts.
  ref: decision 0006
  ref: decision 0008
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","runnerImage":"{runner.test-support}","skipVendorBinaryDownload":true},"manageState":false}' through B
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  @op:workerRunTokenKeys @op:runTrigger @case:success
  Scenario: The server-signed run token of the assignment is admitted, and a worker session reads only public keys
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, FAILED"
    And the answer's last state transition's note is "The run's image has no sh to run its phases in."
    And the relay delivered the run "run" the server's own run token, its signing key kept as "key.one"
    And the relay passed "GET /worker/v1/run-token-keys" answered 200
    And the relay passed "GET /worker/v1/runs/{run}/spec" answered 200
    When the launcher "probe" registers with the key of "csr" and the certificate "cert" through A
    And token "probe" sends "GET /worker/v1/run-token-keys" through B
    Then it answers 200
    And the answer names the run tokens' issuer and audience and the public key kept as "key.one", with no private member

  @op:workerRunTokenKeys @case:denial
  Scenario: Only a worker session reads the run token keys
    When token "h" sends "GET /worker/v1/run-token-keys" through A
    Then it answers 401
    When a caller with no token sends "GET /worker/v1/run-token-keys" through B
    Then it answers 401

  @op:workerRunTokenKeys @case:failure
  Scenario: A deregistered worker's session reads no run token keys
    Given the launcher "probe" registers with the key of "csr" and the certificate "cert" through A
    And token "probe" sends "POST /worker/v1/deregister" through B
    And it answers 204
    When token "probe" sends "GET /worker/v1/run-token-keys" through A
    Then it answers 401

  @op:runTrigger @case:failure
  Scenario Outline: A run token <forgery> is refused before the run's specification or container
    Given the relay delivers the next run token <forgery>
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note is "<note>"
    And the run "run" never started a container
    And the relay passed no "GET /worker/v1/runs/{run}/spec"
    And the launcher "l" logged no JWT
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

    Examples:
      | forgery                                   | note                                                                                   |
      | with its signature altered                | signed run admission refused: the run token's signature does not verify                |
      | with its run claim altered                | signed run admission refused: the run token's signature does not verify                |
      | signed by an unrelated installation's key | signed run admission refused: the run token names no key the server verifies with     |

  @op:runTrigger @case:failure
  Scenario: A genuine token of another kind is refused as no run token
    Given the relay delivers the token kept as "h" in place of the next run token
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note is "signed run admission refused: the run token's audience is not the runner's"
    And the run "run" never started a container
    And the relay passed no "GET /worker/v1/runs/{run}/spec"

  @op:runTrigger @case:failure
  Scenario: Another account's genuine run token is refused as another run's
    Given the stranger key is exchanged through A for token "x"
    And a certificate signing request for a P-256 key is kept as "foreigncsr"
    And token "x" sends "POST /api/v1/worker-pools" with '{"name":"Foreign","space":"legacy","certificateSigningRequest":"{foreigncsr}"}' through A
    And the answer's id names "foreign"
    And the answer's pool token names the pool "foreign" and chains to the pool CA, its serial kept as "foreigncert"
    And token "x" sends "POST /api/v1/stacks" with '{"input":{"name":"Elsewhere","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{foreign}","runnerImage":"{runner.test-support}","skipVendorBinaryDownload":true},"manageState":false}' through B
    And the launcher "fl" registers with the key of "foreigncsr" and the certificate "foreigncert" through A
    And token "x" sends "POST /api/v1/stacks/elsewhere/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "frun"
    And token "x" eventually reads the run "frun" in the state "READY" through A
    And token "fl" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "frun", its run token kept as "ft"
    And the relay delivers the token kept as "ft" in place of the next run token
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note is "signed run admission refused: the run token names another run"
    And the run "run" never started a container
    And the relay passed no "GET /worker/v1/runs/{run}/spec"
    And token "x" eventually reads the run "frun" in the state "PREPARING" through B

  @op:runTrigger @case:success
  Scenario: A run token signed by a rotated key is admitted once the launcher reads the keys again
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "first"
    Then token "h" eventually reads the run "first" in the state "FAILED" through A
    And the relay delivered the run "first" the server's own run token, its signing key kept as "key.one"
    When the api-token signing keys rotate until a token exchanged through A is signed by a key other than "key.one"
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "second"
    Then token "h" eventually reads the run "second" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, FAILED"
    And the relay delivered the run "second" the server's own run token, signed by a key other than "key.one"
    And the relay passed "GET /worker/v1/run-token-keys" answered 200 2 times
