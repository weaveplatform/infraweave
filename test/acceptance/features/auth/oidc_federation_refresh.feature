@postgres @objectstore @vcs @docker @tofu @oidc
Feature: A long phase's OIDC federation token is refreshed
  Federation tokens here live 15 minutes and 15 seconds, so the runner
  asks for a new one 15 seconds after the handover mints the first,
  fifteen minutes before it expires, through the worker API's
  POST /worker/v1/runs/{run}/oidc-token with its run token. The runner
  masks the new token, writes it beside /mnt/workspace/infraweave.oidc
  and renames it over the file, which stays mode 0400 and owned by the
  runner's user, while INFRAWEAVE_OIDC_TOKEN keeps the phase start's. The
  new token verifies against the served discovery document and JWKS with
  the first one's claims, a fresh jti and a later exp. The route answers
  only the run its run token names, while the run is in a phase: 409
  invalid-state while it prepares, waits for confirmation or has ended,
  401 to another run's path, a worker's session token and an API token,
  and 403 feature-not-in-tier once the account's tier no longer includes
  oidc-federation. Each stack's hooks print a token with its dots
  replaced by commas, which the masker does not match, so the scenario
  reads it. The home key is space-admin on the account's top space.
  Stacks live in legacy; the repository's configuration has a
  terraform_data resource alone, so no provider is downloaded.
  ref: decision 0006
  ref: decision 0008

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database, with an operator key and federation tokens lasting "15m15s"
    And the operator key is exchanged through A for token "op"
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = "one"\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"

  @op:runTrigger @op:workerRunOIDCToken @case:success
  Scenario: A phase that outlives its federation token sees the file replaced with a new token, while INFRAWEAVE_OIDC_TOKEN keeps the first
    Given token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}},"beforeInit":["f=/mnt/workspace/infraweave.oidc","t=$(printf %s \"$INFRAWEAVE_OIDC_TOKEN\" | tr . ,)","echo \"oidc token: $t\"","i=0; while [ \"$(cat $f)\" = \"$INFRAWEAVE_OIDC_TOKEN\" ] && [ $i -lt 90 ]; do sleep 1; i=$((i+1)); done","echo \"oidc refreshed: $(tr . , < $f)\"","echo \"oidc file: $(stat -c %a:%u $f)\"","[ \"$(cat $f)\" != \"$INFRAWEAVE_OIDC_TOKEN\" ] && echo \"oidc env: kept\"","echo \"oidc new: $(cat $f)\""]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FINISHED" through B
    And token "h" reads the run "run"'s INITIALIZING log through A, which holds "oidc file: 400:1983\noidc env: kept\noidc new: *****" and not "Error"
    And token "h" reads the run "run"'s INITIALIZING log through B, whose federation token is kept as "first"
    And token "h" reads the run "run"'s INITIALIZING log through A, whose refreshed federation token is kept as "second"
    And the federation token "first" verifies against the issuer "http://infraweave.test" discovered through A, living "15m15s", with the claims:
      | iss        | "http://infraweave.test" |
      | aud        | "infraweave.test/acme" |
      | sub        | "space:legacy:stack:infra:run_type:PROPOSED:scope:read" |
      | spaceId    | "legacy" |
      | spacePath  | "/acme/legacy" |
      | callerType | "stack" |
      | callerId   | "infra" |
      | runType    | "PROPOSED" |
      | runId      | "{run}" |
      | scope      | "read" |
    And the federation token "second" verifies against the issuer "http://infraweave.test" discovered through B, living "15m15s", with the claims:
      | iss        | "http://infraweave.test" |
      | aud        | "infraweave.test/acme" |
      | sub        | "space:legacy:stack:infra:run_type:PROPOSED:scope:read" |
      | spaceId    | "legacy" |
      | spacePath  | "/acme/legacy" |
      | callerType | "stack" |
      | callerId   | "infra" |
      | runType    | "PROPOSED" |
      | runId      | "{run}" |
      | scope      | "read" |
    And the federation token "second" expires after the federation token "first"
    And no object in the scenario's bucket holds the federation token "first"
    And no object in the scenario's bucket holds the federation token "second"

  @op:workerRunOIDCToken @op:runDiscard @case:denial
  Scenario: The route reissues a token only to its own run's token while the run is in a phase of an account that may federate
    Given the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}}},"manageState":false}' through B
    And the launcher "w" registers with the key of "csr" and the certificate "cert" through A
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through A
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "run", its run token kept as "rt"
    And token "rt" sends "GET /worker/v1/runs/{run}/spec" through A
    And it answers 200
    When token "rt" sends "POST /worker/v1/runs/{run}/oidc-token" through B
    Then it answers a 409 problem "invalid-state"
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"started","state":"INITIALIZING","stateVersion":3}' through A
    Then it answers 200
    When token "rt" sends "POST /worker/v1/runs/{run}/oidc-token" through B
    Then it answers 200
    And the answer's federation token is kept as "reissued"
    And the federation token "reissued" verifies against the issuer "http://infraweave.test" discovered through A, living "15m15s", with the claims:
      | iss        | "http://infraweave.test" |
      | aud        | "infraweave.test/acme" |
      | sub        | "space:legacy:stack:infra:run_type:TRACKED:scope:read" |
      | spaceId    | "legacy" |
      | spacePath  | "/acme/legacy" |
      | callerType | "stack" |
      | callerId   | "infra" |
      | runType    | "TRACKED" |
      | runId      | "{run}" |
      | scope      | "read" |
    When token "rt" sends "POST /worker/v1/runs/{run}/oidc-token" through A
    Then it answers 200
    And the answer's federation token is kept as "again"
    And the federation token "again" verifies against the issuer "http://infraweave.test" discovered through B, living "15m15s", with the claims:
      | sub        | "space:legacy:stack:infra:run_type:TRACKED:scope:read" |
      | runId      | "{run}" |
      | scope      | "read" |
    When token "rt" sends "POST /worker/v1/runs/01KACME000000000000000000A/oidc-token" through A
    Then it answers a 401 problem "unauthenticated"
    When token "w" sends "POST /worker/v1/runs/{run}/oidc-token" through B
    Then it answers a 401 problem "unauthenticated"
    When token "h" sends "POST /worker/v1/runs/{run}/oidc-token" through A
    Then it answers a 401 problem "unauthenticated"
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"INITIALIZING","stateVersion":4}' through B
    Then the answer's state is '"PLANNING"'
    When token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"succeeded","state":"PLANNING","stateVersion":5,"delta":{"add":1,"change":0,"delete":0,"import":0,"outputs":false}}' through A
    Then the answer's state is '"UNCONFIRMED"'
    When token "rt" sends "POST /worker/v1/runs/{run}/oidc-token" through B
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "POST /api/v1/stacks/infra/runs/discard" with '{"run":"{run}"}' through A
    Then the answer's state is '"DISCARDED"'
    When token "rt" sends "POST /worker/v1/runs/{run}/oidc-token" through A
    Then it answers a 409 problem "invalid-state"

  @op:workerRunOIDCToken @case:denial
  Scenario: A run whose account loses oidc-federation mid-phase is refused a new token
    Given the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}}},"manageState":false}' through B
    And the launcher "w" registers with the key of "csr" and the certificate "cert" through A
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through A
    And token "w" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    And the answer assigns the run "run", its run token kept as "rt"
    And token "rt" sends "GET /worker/v1/runs/{run}/spec" through A
    And it answers 200
    And token "rt" sends "POST /worker/v1/runs/{run}/reports" with '{"event":"started","state":"INITIALIZING","stateVersion":3}' through B
    And it answers 200
    When token "op" gives the home organization a copy of the tier "Business" without "oidc-federation", with billing on, through A
    And token "rt" sends "POST /worker/v1/runs/{run}/oidc-token" through A
    Then it answers a 403 problem "feature-not-in-tier"
