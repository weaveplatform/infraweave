@kubernetes-lab @kubernetes @postgres @vcs @tofu
Feature: A long phase's OIDC federation token is refreshed in a Kubernetes Pod
  A Managed pool's run Pod refreshes its federation token as a Docker
  run container does. Federation tokens here live 15 minutes and 15
  seconds, so the runner in the Pod asks the worker API for a new one 15
  seconds after the handover mints the first, through the scenario's
  HTTPS front, masks it, and renames it over
  /mnt/workspace/infraweave.oidc, which stays mode 0400 and owned by UID
  1983, while INFRAWEAVE_OIDC_TOKEN keeps the phase start's. The new
  token verifies against the served discovery document and JWKS with the
  first one's claims and a later exp, and no object, Pod, Worker resource
  or controller log line holds either. The stack's hooks print a token
  with its dots replaced by commas, which the masker does not match, so
  the scenario reads it. Repositories are Gitea's; the home key is
  space-admin on the account's top space.
  ref: decision 0006
  ref: decision 0008

  Background:
    Given the owned Terraform Kubernetes API and namespace are ready
    And the canonical worker CRDs are established in the owned cluster
    And a controller namespace "home" holds the home management key
    And run Pods reach the scenario's servers, and the objects they sign, through the HTTPS front
    And the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database, with an operator key and federation tokens lasting "15m15s"
    And the home key is exchanged through A for token "h"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = "one"\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And 1 worker controller watch the controller namespaces
    And the Managed WorkerPool "build" of capacity 1 in the space "team" is applied in "home"
    And the WorkerPool "build" eventually has 1 registered slots, its pool kept as "build"
    And the WorkerPool "build" eventually reports ExecutorAvailable True "LauncherRunning"

  @op:runTrigger @op:workerRunOIDCToken @case:success
  Scenario: A run Pod's phase that outlives its federation token sees the file replaced with a new token
    Given token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}","workerPool":"{build}","vendorConfig":{"opentofu":{}},"beforeInit":["f=/mnt/workspace/infraweave.oidc","t=$(printf %s \"$INFRAWEAVE_OIDC_TOKEN\" | tr . ,)","echo \"oidc token: $t\"","i=0; while [ \"$(cat $f)\" = \"$INFRAWEAVE_OIDC_TOKEN\" ] && [ $i -lt 90 ]; do sleep 1; i=$((i+1)); done","echo \"oidc refreshed: $(tr . , < $f)\"","echo \"oidc file: $(stat -c %a:%u $f)\"","[ \"$(cat $f)\" != \"$INFRAWEAVE_OIDC_TOKEN\" ] && echo \"oidc env: kept\"","echo \"oidc new: $(cat $f)\""]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FINISHED" through B
    And token "h" reads the run "run"'s INITIALIZING log through A, which holds "oidc file: 400:1983\noidc env: kept\noidc new: *****" and not "Error"
    And token "h" reads the run "run"'s INITIALIZING log through B, whose federation token is kept as "first"
    And token "h" reads the run "run"'s INITIALIZING log through A, whose refreshed federation token is kept as "second"
    And the federation token "first" verifies against the issuer "http://infraweave.test" discovered through A, living "15m15s", with the claims:
      | iss        | "http://infraweave.test" |
      | aud        | "infraweave.test/acme" |
      | sub        | "space:team:stack:infra:run_type:PROPOSED:scope:read" |
      | spaceId    | "team" |
      | spacePath  | "/acme/team" |
      | callerType | "stack" |
      | callerId   | "infra" |
      | runType    | "PROPOSED" |
      | runId      | "{run}" |
      | scope      | "read" |
    And the federation token "second" verifies against the issuer "http://infraweave.test" discovered through B, living "15m15s", with the claims:
      | iss        | "http://infraweave.test" |
      | aud        | "infraweave.test/acme" |
      | sub        | "space:team:stack:infra:run_type:PROPOSED:scope:read" |
      | spaceId    | "team" |
      | spacePath  | "/acme/team" |
      | callerType | "stack" |
      | callerId   | "infra" |
      | runType    | "PROPOSED" |
      | runId      | "{run}" |
      | scope      | "read" |
    And the federation token "second" expires after the federation token "first"
    And the run "run" eventually has no Pod
    And no object in the scenario's bucket holds the federation token "first"
    And no object in the scenario's bucket holds the federation token "second"
