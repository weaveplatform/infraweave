@postgres @objectstore @vcs
Feature: A run's CPU profile of a state downloads once its worker records it
  A worker that profiles uploads each state's CPU profile through a signed
  URL and records it through the worker API, before it reports the state's
  end. A caller who can read the run downloads a state's profile through
  a signed URL: ongoing while the run is in the state and has recorded
  none, expired when the state recorded none, and deleted once the run's
  logs were deleted. Here the scenario is the worker. The run's in-run API
  token does not read the profile. The release indexes serve the recorded
  fixtures, so the run's specification names a Terraform release. The
  home key is space-admin on the account's top space; the member key holds
  no binding. Stack infra lives in legacy and runs on the pool.
  ref: decision 0008
  ref: decision 0016

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
    And the specification's sealed part opens with the key of "csr" to an in-run API token, kept as "it"
    And token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"started","state":"INITIALIZING","stateVersion":3}' through B
    And token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"INITIALIZING","stateVersion":4}' through A
    And the answer's state is '"PLANNING"'

  @op:runCpuProfileDownloadURL @case:success
  Scenario: A state's profile downloads once recorded, a state with none answers expired, and deleted logs answer deleted
    When token "h" sends "GET /api/v1/runs/{run}/cpu-profile-download-url?state=PLANNING" through A
    Then it answers 200
    And the answer's ongoing is 'true'
    And the answer's expired is 'false'
    And the answer leaves out "url"
    When token "rt" uploads the observability object "cpu-PLANNING.pprof" of the run "run" in state version 5 holding "profile bytes" through B
    And token "h" sends "GET /api/v1/runs/{run}/cpu-profile-download-url?state=PLANNING" through A
    Then it answers 200
    And the answer's url downloads "profile bytes"
    And the answer's ongoing is 'false'
    And the answer's expired is 'false'
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":1,"change":0,"delete":0,"import":0,"outputs":false}}' through B
    And the answer's state is '"UNCONFIRMED"'
    And token "h" sends "GET /api/v1/runs/{run}/cpu-profile-download-url?state=PLANNING" through B
    Then the answer's url downloads "profile bytes"
    When token "h" sends "GET /api/v1/runs/{run}/cpu-profile-download-url?state=INITIALIZING" through A
    Then it answers 200
    And the answer's expired is 'true'
    And the answer's ongoing is 'false'
    And the answer leaves out "url"
    When token "h" sends "GET /api/v1/runs/{run}/cpu-profile-download-url?state=APPLYING" through B
    Then the answer's expired is 'true'
    When token "h" sends "POST /api/v1/stacks/infra/runs/discard" with '{"run":"{run}"}' through A
    Then the answer's state is '"DISCARDED"'
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/logs-delete" through B
    Then it answers 200
    When token "h" sends "GET /api/v1/runs/{run}/cpu-profile-download-url?state=PLANNING" through A
    Then it answers 200
    And the answer's deleted is 'true'
    And the answer's expired is 'true'
    And the answer leaves out "url"

  @op:runCpuProfileDownloadURL @case:denial
  Scenario: The run's own in-run API token does not read its profile
    When token "it" sends "GET /api/v1/runs/{run}/cpu-profile-download-url?state=PLANNING" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_READ"'

  @op:runCpuProfileDownloadURL @case:failure
  Scenario: A caller who cannot read the run, an unknown run and a request that names no state are refused
    When token "m" sends "GET /api/v1/runs/{run}/cpu-profile-download-url?state=PLANNING" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/runs/01JBQ6Q4V2ZK5Y3W8N7M6P5R4T/cpu-profile-download-url?state=PLANNING" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/runs/{run}/cpu-profile-download-url" through A
    Then it answers a 400 problem "invalid-request"
