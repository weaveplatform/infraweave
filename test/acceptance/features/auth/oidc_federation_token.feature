@run-credentials @postgres @objectstore @vcs @docker @tofu @oidc
Feature: Every run's OIDC federation token
  A run of an account whose tier has oidc-federation receives, at each
  handover that starts a phase, an OIDC federation token signed by the
  active federation key, in INFRAWEAVE_OIDC_TOKEN, set after the stack's
  variables, and in /mnt/workspace/infraweave.oidc, mode 0400 and owned
  by the runner's user, both written before the phase's hooks. A relying
  party verifies it through go-oidc against the served discovery document
  and JWKS. It carries exactly the document's claims: the issuer; the
  audience, the issuer's host followed by the account's slug, so two
  accounts' tokens differ; the account's subject template rendered for the
  run; an hour's lifetime from iat, nbf equal to iat; a fresh ULID jti;
  the stack's space and its path from the account's top space, named by
  the account's slug; the stack as caller; the run's type and ID; and the
  scope. A proposed run's scope is read, as is a tracked run's planning on
  a stack without autodeploy, which receives a new write token for
  applying; a tracked run with autodeploy, a task and a destroy run write.
  The log masks the token, no object of the store holds it, and the
  workspace saved for the plan's confirmation leaves the file out, while a
  file a hook copies it into fails the planning, naming the file. A run of
  an account whose tier lacks oidc-federation receives neither the
  variable nor the file and goes on. Each stack's hooks print the token
  with its dots replaced by commas, which the masker does not match, so
  the scenario reads it. The home key is space-admin on the account's top
  space, and the sibling key on acme-dev's. Stacks live in legacy and keep
  their state in infraweave; the repository's configuration has a
  terraform_data resource alone, so no provider is downloaded.
  ref: decision 0006
  ref: decision 0008
  ref: decision 0021

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database, with an operator key
    And the operator key is exchanged through A for token "op"
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = "one"\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  @op:runTrigger @op:runConfirm @op:runLogs @case:success
  Scenario: A tracked run plans with a read token and applies with a new write token, each verified against the served discovery document
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}},"beforeInit":["t=$(printf %s \"$INFRAWEAVE_OIDC_TOKEN\" | tr . ,)","echo \"oidc token: $t\"","echo \"oidc env: $INFRAWEAVE_OIDC_TOKEN\"","echo \"oidc file: $(stat -c %a:%u /mnt/workspace/infraweave.oidc)\"","[ \"$(cat /mnt/workspace/infraweave.oidc)\" = \"$INFRAWEAVE_OIDC_TOKEN\" ] && echo \"oidc file: same\""]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And token "h" reads the run "run"'s INITIALIZING log through B, which holds "oidc env: *****\noidc file: 400:1983\noidc file: same" and not "Error"
    And token "h" reads the run "run"'s PLANNING log through A, which holds "infraweave: saved the workspace for the plan's confirmation" and not "Error"
    And token "h" reads the run "run"'s INITIALIZING log through A, whose federation token is kept as "plan"
    And the federation token "plan" verifies against the issuer "http://infraweave.test" discovered through B, with the claims:
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
    When token "h" sends "POST /api/v1/stacks/infra/runs/confirm" with '{"run":"{run}"}' through A
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And token "h" reads the run "run"'s APPLYING log through A, which holds "oidc env: *****\noidc file: 400:1983\noidc file: same" and not "Error"
    And token "h" reads the run "run"'s APPLYING log through B, whose federation token is kept as "apply"
    And the federation token "apply" verifies against the issuer "http://infraweave.test" discovered through A, with the claims:
      | iss        | "http://infraweave.test" |
      | aud        | "infraweave.test/acme" |
      | sub        | "space:legacy:stack:infra:run_type:TRACKED:scope:write" |
      | spaceId    | "legacy" |
      | spacePath  | "/acme/legacy" |
      | callerType | "stack" |
      | callerId   | "infra" |
      | runType    | "TRACKED" |
      | runId      | "{run}" |
      | scope      | "write" |
    And no object in the scenario's bucket holds the federation token "plan"
    And no object in the scenario's bucket holds the federation token "apply"

  @op:runTrigger @op:taskCreate @op:runDestroyTrigger @op:accountSetOIDCSubjectTemplate @case:success
  Scenario: Proposed, autodeployed, task and destroy runs carry their scopes, and a custom subject template is the next run's sub
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}},"autodeploy":true,"beforeInit":["t=$(printf %s \"$INFRAWEAVE_OIDC_TOKEN\" | tr . ,)","echo \"oidc token: $t\"","echo \"oidc env: $INFRAWEAVE_OIDC_TOKEN\"","echo \"oidc file: $(stat -c %a:%u /mnt/workspace/infraweave.oidc)\"","[ \"$(cat /mnt/workspace/infraweave.oidc)\" = \"$INFRAWEAVE_OIDC_TOKEN\" ] && echo \"oidc file: same\""]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And the answer's id names "proposed"
    Then token "h" eventually reads the run "proposed" in the state "FINISHED" through B
    And token "h" reads the run "proposed"'s INITIALIZING log through A, whose federation token is kept as "proposed"
    And the federation token "proposed" verifies against the issuer "http://infraweave.test" discovered through A, with the claims:
      | iss        | "http://infraweave.test" |
      | aud        | "infraweave.test/acme" |
      | sub        | "space:legacy:stack:infra:run_type:PROPOSED:scope:read" |
      | spaceId    | "legacy" |
      | spacePath  | "/acme/legacy" |
      | callerType | "stack" |
      | callerId   | "infra" |
      | runType    | "PROPOSED" |
      | runId      | "{proposed}" |
      | scope      | "read" |
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "tracked"
    Then token "h" eventually reads the run "tracked" in the state "FINISHED" through A
    And token "h" reads the run "tracked"'s INITIALIZING log through B, whose federation token is kept as "tracked"
    And the federation token "tracked" verifies against the issuer "http://infraweave.test" discovered through B, with the claims:
      | iss        | "http://infraweave.test" |
      | aud        | "infraweave.test/acme" |
      | sub        | "space:legacy:stack:infra:run_type:TRACKED:scope:write" |
      | spaceId    | "legacy" |
      | spacePath  | "/acme/legacy" |
      | callerType | "stack" |
      | callerId   | "infra" |
      | runType    | "TRACKED" |
      | runId      | "{tracked}" |
      | scope      | "write" |
    When "POST /api/v1/account/set-oidc-subject-template" through A with token "h" and '{"template":"{spacePath}|{callerType}|{callerId}|{runType}|{scope}"}' answers '"{spacePath}|{callerType}|{callerId}|{runType}|{scope}"'
    And token "h" sends "POST /api/v1/stacks/infra/tasks" with '{"command":"echo task"}' through B
    Then it answers 201
    And the answer's id names "task"
    And token "h" eventually reads the run "task" in the state "FINISHED" through A
    And token "h" reads the run "task"'s INITIALIZING log through B, whose federation token is kept as "task"
    And the federation token "task" verifies against the issuer "http://infraweave.test" discovered through A, with the claims:
      | iss        | "http://infraweave.test" |
      | aud        | "infraweave.test/acme" |
      | sub        | "/acme/legacy\|stack\|infra\|TASK\|write" |
      | spaceId    | "legacy" |
      | spacePath  | "/acme/legacy" |
      | callerType | "stack" |
      | callerId   | "infra" |
      | runType    | "TASK" |
      | runId      | "{task}" |
      | scope      | "write" |
    When token "h" sends "POST /api/v1/stacks/infra/destroy-runs" through A
    Then it answers 201
    And the answer's id names "destroy"
    And token "h" eventually reads the run "destroy" in the state "FINISHED" through B
    And token "h" reads the run "destroy"'s INITIALIZING log through A, whose federation token is kept as "destroy"
    And the federation token "destroy" verifies against the issuer "http://infraweave.test" discovered through B, with the claims:
      | iss        | "http://infraweave.test" |
      | aud        | "infraweave.test/acme" |
      | sub        | "/acme/legacy\|stack\|infra\|DESTROY\|write" |
      | spaceId    | "legacy" |
      | spacePath  | "/acme/legacy" |
      | callerType | "stack" |
      | callerId   | "infra" |
      | runType    | "DESTROY" |
      | runId      | "{destroy}" |
      | scope      | "write" |

  @op:runTrigger @case:success
  Scenario: Another account's run carries the audience and space path of its own slug
    Given the sibling key is exchanged through A for token "s"
    And a certificate signing request for a P-256 key is kept as "devcsr"
    And token "s" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{devcsr}"}' through A
    And the answer's id names "devpool"
    And token "s" starts the launcher "dl" of the pool "devpool" with the key of "devcsr" through A
    And token "s" eventually reads the worker "dl" of the pool "devpool" as "IDLE" through B
    And token "s" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{devpool}","vendorConfig":{"opentofu":{}},"beforeInit":["t=$(printf %s \"$INFRAWEAVE_OIDC_TOKEN\" | tr . ,)","echo \"oidc token: $t\"","echo \"oidc env: $INFRAWEAVE_OIDC_TOKEN\"","echo \"oidc file: $(stat -c %a:%u /mnt/workspace/infraweave.oidc)\"","[ \"$(cat /mnt/workspace/infraweave.oidc)\" = \"$INFRAWEAVE_OIDC_TOKEN\" ] && echo \"oidc file: same\""]},"manageState":true}' through B
    When token "s" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And the answer's id names "devrun"
    Then token "s" eventually reads the run "devrun" in the state "FINISHED" through B
    And token "s" reads the run "devrun"'s INITIALIZING log through A, whose federation token is kept as "dev"
    And the federation token "dev" verifies against the issuer "http://infraweave.test" discovered through A, with the claims:
      | iss        | "http://infraweave.test" |
      | aud        | "infraweave.test/acme-dev" |
      | sub        | "space:legacy:stack:infra:run_type:PROPOSED:scope:read" |
      | spaceId    | "legacy" |
      | spacePath  | "/acme-dev/legacy" |
      | callerType | "stack" |
      | callerId   | "infra" |
      | runType    | "PROPOSED" |
      | runId      | "{devrun}" |
      | scope      | "read" |

  @op:runTrigger @case:denial
  Scenario: A run of an account whose tier lacks oidc-federation receives neither the variable nor the file, and goes on
    Given token "op" gives the home organization a copy of the tier "Business" without "oidc-federation", with billing on, through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}},"beforeInit":["echo \"oidc variable: ${INFRAWEAVE_OIDC_TOKEN:-unset}\"","[ -e /mnt/workspace/infraweave.oidc ] || echo \"oidc file: none\""]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FINISHED" through B
    And token "h" reads the run "run"'s INITIALIZING log through A, which holds "oidc variable: unset\noidc file: none" and not "Error"

  @op:runTrigger @case:failure
  Scenario: A hook that copies the token into the workspace fails the planning, naming the file but not the token
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}},"beforeInit":["t=$(printf %s \"$INFRAWEAVE_OIDC_TOKEN\" | tr . ,)","echo \"oidc token: $t\"","echo \"oidc env: $INFRAWEAVE_OIDC_TOKEN\"","echo \"oidc file: $(stat -c %a:%u /mnt/workspace/infraweave.oidc)\"","[ \"$(cat /mnt/workspace/infraweave.oidc)\" = \"$INFRAWEAVE_OIDC_TOKEN\" ] && echo \"oidc file: same\""],"afterPlan":["cp /mnt/workspace/infraweave.oidc leak.txt"]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FAILED"
    And the answer's last state transition's note is "The workspace could not be saved for the plan's confirmation: archive the workspace: a file of the workspace holds one of the run's credentials: the file source/leak.txt."
    And token "h" reads the run "run"'s INITIALIZING log through A, whose federation token is kept as "leaked"
    And no object in the scenario's bucket holds the federation token "leaked"
