@plan-sanitization @postgres @objectstore @vcs
Feature: Legacy workers cannot index raw plans after a server upgrade
  Registration capability rejects old launchers. These separate protocol tests
  model a run token already issued before the upgrade, and a sealed raw object
  already uploaded through a previously signed URL. Neither path can record
  plan.json without the matching explicit sanitization receipt. The real-worker
  transformation is exercised by sanitization.feature.
  ref: decision 0008
  ref: decision 0009

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And the member key is exchanged through A for token "m"
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}"},"manageState":false}' through B
    And the launcher "l" registers with the key of "csr" and the certificate "cert" through A
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through A
    And token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "run", its run token kept as "rt"
    And token "rt" sends "GET /worker/v1/runs/{run}/spec" through A
    And the specification names the run "run" of the home account, kept as "acct"
    And the specification's sealed part opens with the key of "csr" to an in-run API token, kept as "it"
    And the specification's sealed part opens with the key of "csr" to the run's artifact key, kept as "ak"
    And token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"started","state":"INITIALIZING","stateVersion":3}' through B
    And it answers 200
    And token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"INITIALIZING","stateVersion":4}' through A
    And the answer's state is '"PLANNING"'


  @case:denial
  Scenario: Existing run tokens and previously uploaded objects need the resolved mode receipt
    When token "rt" sends "POST /worker/v1/runs/{run}/upload-urls" with '{"stateVersion":5,"uploads":[{"kind":"artifact","name":"plan.json","size":9,"sha256":"0000000000000000000000000000000000000000000000000000000000000000"}]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/uploads/0/planSanitization" as "rule"
    When token "rt" sends "POST /worker/v1/runs/{run}/upload-urls" with '{"stateVersion":5,"uploads":[{"kind":"artifact","name":"plan.json","planSanitization":"NONE","size":9,"sha256":"0000000000000000000000000000000000000000000000000000000000000000"}]}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/uploads/0/planSanitization" as "rule"
    Given a raw plan is sealed and uploaded without recording for the run "run" using token "rt" and artifact key "ak" through A
    When token "rt" sends "POST /worker/v1/runs/{run}/artifacts" with '{"stateVersion":5,"artifacts":[{"kind":"artifact","name":"plan.json"}]}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/artifacts/0/planSanitization" as "rule"
    And the run "run" recorded 0 worker plan artifacts
    When token "rt" sends "POST /worker/v1/runs/{run}/artifacts" with '{"stateVersion":5,"artifacts":[{"kind":"artifact","name":"plan.json","planSanitization":"NONE"}]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/artifacts/0/planSanitization" as "rule"
    And the run "run" recorded 0 worker plan artifacts
    And no object in the scenario's bucket holds "dummy-plan-secret-642", of at least 1 objects
