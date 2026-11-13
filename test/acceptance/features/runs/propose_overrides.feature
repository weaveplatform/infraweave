@postgres @vcs
Feature: A proposed run sets environment variables over its stack's
  A developer proposes a run with environment variables that the run sets
  over its stack's, for that run alone. An override replaces the stack's
  variable of the same name and stays write-only when that variable is;
  a name the stack does not set is set in the clear. The overrides are
  kept sealed: a run's specification is the one answer that carries them,
  a write-only one in its sealed part alone, and no server logs them. A
  proposal with overrides creates a run each time, since no other run
  shares them; one without answers the proposed run of the same commit
  that has not ended and has no overrides. A space writer proposes; a
  space reader is refused RUN_PROPOSE_WITH_OVERRIDES, and a stack the
  caller cannot read, or another account's, answers as one that does not
  exist. An override with no name, a name holding =, a name given twice,
  and a name infraweave sets for the run, TF_VAR_infraweave_* or
  INFRAWEAVE_*, are refused at the override's key. The release indexes
  serve the recorded fixtures. The home key is space-admin on the
  account's top space; the member key holds only the bindings a scenario
  gives it. Stack infra lives in legacy and runs on the pool, and sets
  REGION in the clear and TOKEN write-only; stack hidden lives in team.
  ref: decision 0008
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}"},"manageState":false}' through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Hidden","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"{team}"},"manageState":false}' through A
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"REGION","type":"ENVIRONMENT_VARIABLE","value":"eu","writeOnly":false}' through A
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TOKEN","type":"ENVIRONMENT_VARIABLE","value":"s3cret","writeOnly":true}' through B
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A

  @op:proposeWithOverrides @case:success
  Scenario: A proposal's overrides reach its run's specification, sealed where the stack's variable is, and each proposal creates a run
    Given the launcher "l" registers with the key of "csr" and the certificate "cert" through A
    When token "h" sends "POST /api/v1/stacks/infra/proposed-runs" with '{"environmentVarsOverrides":[{"key":"REGION","value":"us"},{"key":"TOKEN","value":"t0psecret"},{"key":"EXTRA","value":"pl41n-extra"}]}' through A
    Then it answers 201
    And the answer's type is '"PROPOSED"'
    And the answer's state is '"QUEUED"'
    And the answer's commits is '["{seed}"]'
    And the answer's id names "run"
    And neither the answer nor a server's log names "t0psecret"
    And neither the answer nor a server's log names "pl41n-extra"
    And token "h" eventually reads the run "run" in the state "READY" through B
    And token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then the answer assigns the run "run", its run token kept as "rt"
    When token "rt" sends "GET /worker/v1/runs/{run}/spec" through A
    Then it answers 200
    And the specification's variable "REGION" is "us"
    And the specification's variable "EXTRA" is "pl41n-extra"
    And the specification's variable "TOKEN" is write-only
    And the specification's sealed part opens with the key of "csr" to the secret "TOKEN" as "t0psecret"
    And neither the answer nor a server's log names "t0psecret"
    When token "h" sends "GET /api/v1/runs/{run}" through B
    Then it answers 200
    And neither the answer nor a server's log names "pl41n-extra"
    When token "h" sends "POST /api/v1/stacks/infra/proposed-runs" with '{"environmentVarsOverrides":[{"key":"REGION","value":"us"},{"key":"TOKEN","value":"t0psecret"},{"key":"EXTRA","value":"pl41n-extra"}]}' through B
    Then it answers 201
    And the answer's id names "again"
    When token "h" sends "POST /api/v1/stacks/infra/proposed-runs" with '{"commitSha":"{seed}"}' through A
    Then it answers 201
    And the answer's id names "plain"
    When token "h" sends "POST /api/v1/stacks/infra/proposed-runs" with '{}' through B
    Then it answers 201
    And the answer's id is '"{plain}"'
    When token "h" sends "GET /api/v1/stacks/infra/proposed-runs" through A
    Then the answer's items are the runs "plain, again, run"

  @op:proposeWithOverrides @case:denial
  Scenario: A space reader is refused, and a stack the caller cannot read does not exist
    Given the member key is space-reader on the space "legacy"
    And the sibling key is exchanged through A for token "sib"
    When token "m" sends "POST /api/v1/stacks/infra/proposed-runs" with '{"environmentVarsOverrides":[{"key":"REGION","value":"us"}]}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"RUN_PROPOSE_WITH_OVERRIDES"'
    When token "m" sends "POST /api/v1/stacks/hidden/proposed-runs" with '{"environmentVarsOverrides":[{"key":"REGION","value":"us"}]}' through B
    Then it answers a 404 problem "not-found"
    When token "sib" sends "POST /api/v1/stacks/infra/proposed-runs" with '{"environmentVarsOverrides":[{"key":"REGION","value":"us"}]}' through A
    Then it answers a 404 problem "not-found"
    Given the member key is space-writer on the space "legacy"
    When token "m" sends "POST /api/v1/stacks/infra/proposed-runs" with '{"environmentVarsOverrides":[{"key":"REGION","value":"us"}]}' through B
    Then it answers 201
    And the answer's type is '"PROPOSED"'

  @op:proposeWithOverrides @case:failure
  Scenario: An override of a variable infraweave sets, with no name, with = or given twice, an unknown commit and a value left out are refused
    When token "h" sends "POST /api/v1/stacks/infra/proposed-runs" with '{"environmentVarsOverrides":[{"key":"INFRAWEAVE_API_TOKEN","value":"forged"}]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/environmentVarsOverrides/0/key" as "rule"
    When token "h" sends "POST /api/v1/stacks/infra/proposed-runs" with '{"environmentVarsOverrides":[{"key":"REGION","value":"us"},{"key":"TF_VAR_infraweave_local_preview","value":"true"}]}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/environmentVarsOverrides/1/key" as "rule"
    When token "h" sends "POST /api/v1/stacks/infra/proposed-runs" with '{"environmentVarsOverrides":[{"key":"","value":"1"}]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/environmentVarsOverrides/0/key" as "required"
    When token "h" sends "POST /api/v1/stacks/infra/proposed-runs" with '{"environmentVarsOverrides":[{"key":"A=B","value":"1"}]}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/environmentVarsOverrides/0/key" as "rule"
    When token "h" sends "POST /api/v1/stacks/infra/proposed-runs" with '{"environmentVarsOverrides":[{"key":"REGION","value":"us"},{"key":"REGION","value":"eu"}]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/environmentVarsOverrides/1/key" as "duplicate"
    When token "h" sends "POST /api/v1/stacks/infra/proposed-runs" with '{"commitSha":"0123456789abcdef0123456789abcdef01234567","environmentVarsOverrides":[{"key":"REGION","value":"us"}]}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/commitSha" as "not-found"
    When token "h" sends "POST /api/v1/stacks/infra/proposed-runs" with '{"environmentVarsOverrides":[{"key":"REGION"}]}' through A
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "GET /api/v1/stacks/infra/proposed-runs" through B
    Then the answer's items are the runs ""
