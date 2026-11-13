@postgres @objectstore @vcs
Feature: A run's log is read by state, and downloaded whole once the run leaves the state
  A run's worker writes each state's log in chunks: it uploads a chunk to
  the store through a signed URL and commits it through the worker API,
  which indexes it by state version and sequence number. A caller who can
  read the run reads a state's lines a page at a time, in sequence order,
  while the run is in the state and after it leaves, each line stamped
  with when its chunk began. A page ends before a chunk not yet indexed,
  and its nextToken reads on. Once the run leaves the state, the state's
  chunks are written to one object, whose signed URL the download
  answers. Here the scenario is the worker. The run's in-run API token
  reads neither its run nor its logs. The release indexes serve the
  recorded fixtures, so the run's specification names a Terraform release.
  The home key is space-admin on the account's top space; the member key
  holds no binding. Stack infra lives in legacy and runs on the pool.
  ref: decision 0007
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
    And it answers 200
    And token "rt" writes log chunk 0 of the run "run" in state version 4 at 1800000000 holding "Initializing the backend...\n" through A
    And token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"INITIALIZING","stateVersion":4}' through B
    And the answer's state is '"PLANNING"'

  @op:runLogs @case:success
  Scenario: A state's log pages back every committed line in order, while the run is in the state and after it leaves
    When token "rt" writes log chunk 0 of the run "run" in state version 5 at 1800000010 holding "\x1b[1mPlan:\x1b[0m 1 to add\nline two\n" through B
    And token "rt" writes log chunk 2 of the run "run" in state version 5 at 1800000012 holding "line four\n" through A
    And token "h" sends "GET /api/v1/runs/{run}/logs?state=PLANNING" through B
    Then it answers 200
    And the answer's messages is '[{"message":"\u001b[1mPlan:\u001b[0m 1 to add","timestamp":1800000010},{"message":"line two","timestamp":1800000010}]'
    And the answer's exists is 'true'
    And the answer's ongoing is 'true'
    And the answer's hasMore is 'false'
    And the answer's finished is 'false'
    And the answer's nextToken is kept as "next"
    When token "rt" writes log chunk 1 of the run "run" in state version 5 at 1800000011 holding "line three\n" through B
    And token "h" sends "GET /api/v1/runs/{run}/logs?state=PLANNING&token={next}" through A
    Then the answer's messages is '[{"message":"line three","timestamp":1800000011},{"message":"line four","timestamp":1800000012}]'
    And the answer's nextToken is kept as "next"
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":1,"change":0,"delete":0,"import":0,"outputs":false}}' through B
    And the answer's state is '"UNCONFIRMED"'
    And token "h" sends "GET /api/v1/runs/{run}/logs?state=PLANNING&token={next}" through A
    Then the answer's messages is '[]'
    And the answer's ongoing is 'false'
    And the answer's finished is 'true'
    And the answer leaves out "nextToken"
    When token "h" sends "GET /api/v1/runs/{run}/logs?state=PLANNING&stateVersion=5&sanitize=true" through B
    Then the answer's messages is '[{"message":"Plan: 1 to add","timestamp":1800000010},{"message":"line two","timestamp":1800000010},{"message":"line three","timestamp":1800000011},{"message":"line four","timestamp":1800000012}]'
    And the answer's finished is 'true'
    When token "h" sends "GET /api/v1/runs/{run}/logs?state=INITIALIZING" through A
    Then the answer's messages is '[{"message":"Initializing the backend...","timestamp":1800000000}]'
    When token "h" sends "GET /api/v1/runs/{run}/logs?state=APPLYING" through B
    Then it answers 200
    And the answer's exists is 'false'
    And the answer's finished is 'true'
    And the answer's messages is '[]'

  @op:runLogsDownloadURL @case:success
  Scenario: Once the run leaves a state, its log downloads whole, the chunks' bytes in order
    When token "rt" writes log chunk 0 of the run "run" in state version 5 at 1800000010 holding "Plan: 1 to add\n" through A
    And token "rt" writes log chunk 1 of the run "run" in state version 5 at 1800000011 holding "done\n" through B
    And token "h" sends "GET /api/v1/runs/{run}/logs-download-url?state=PLANNING" through A
    Then it answers 200
    And the answer's ongoing is 'true'
    And the answer's expired is 'false'
    And the answer leaves out "url"
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":1,"change":0,"delete":0,"import":0,"outputs":false}}' through B
    Then token "h" eventually reads a download URL of the run "run"'s PLANNING log through A
    And the answer's url downloads "Plan: 1 to add\ndone\n"
    And the answer's ongoing is 'false'
    And the answer's expired is 'false'
    When token "h" eventually reads a download URL of the run "run"'s INITIALIZING log through B
    Then the answer's url downloads "Initializing the backend...\n"
    When token "h" sends "GET /api/v1/runs/{run}/logs-download-url?state=PREPARING" through A
    Then the answer's expired is 'true'
    And the answer's ongoing is 'false'
    When token "h" sends "GET /api/v1/runs/{run}/logs-download-url?state=APPLYING" through B
    Then the answer's expired is 'true'

  @op:runLogs @op:runLogsDownloadURL @op:run @case:denial
  Scenario: The run's own in-run API token reads its stack while the run plans, but neither the run nor its logs
    When token "it" sends "GET /api/v1/stacks/infra" through A
    Then it answers 200
    When token "it" sends "GET /api/v1/runs/{run}/logs?state=INITIALIZING" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_READ"'
    When token "it" sends "GET /api/v1/runs/{run}/logs-download-url?state=INITIALIZING" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_READ"'
    When token "it" sends "GET /api/v1/runs/{run}" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_READ"'

  @op:runLogs @op:runLogsDownloadURL @case:failure
  Scenario: A caller who cannot read the run, an unknown run, and a token that is not this log's are refused
    When token "m" sends "GET /api/v1/runs/{run}/logs?state=PLANNING" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/runs/{run}/logs-download-url?state=PLANNING" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/runs/01JBQ6Q4V2ZK5Y3W8N7M6P5R4T/logs?state=PLANNING" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/runs/01JBQ6Q4V2ZK5Y3W8N7M6P5R4T/logs-download-url?state=PLANNING" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/runs/{run}/logs?state=PLANNING" through A
    And the answer's nextToken is kept as "planning"
    And token "h" sends "GET /api/v1/runs/{run}/logs?state=INITIALIZING&token={planning}" through B
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "GET /api/v1/runs/{run}/logs?state=PLANNING&token=garbage" through A
    Then it answers a 400 problem "invalid-cursor"
