@postgres @vcs
Feature: Environment templates rendered into a run's specification
  A variable's ${{ … }} templates render when a worker of the run's
  private pool fetches the run's specification with its run token, against
  the variables the stack resolves to, the stack and the run. A value that
  reads a write-only variable is itself a secret: it arrives write-only,
  by name alone, and its value only in the sealed part, which the pool's
  private key opens for the run's state version, with the in-run API token
  and the run's artifact key. The home key is space-admin on the account's
  top space. Stack infra lives in legacy and runs on the pool, with the
  variables REGION, URL, the write-only TOKEN and AUTH, which reads it.
  infraweave's own metadata variables arrive beside them: a run triggered
  on the stack is for its tracked branch and previews no local workspace.
  The release indexes serve the recorded fixtures, so the run's
  specification names a Terraform release.
  ref: decision 0024
  ref: decision 0008

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
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "first"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}"},"manageState":false}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"REGION","type":"ENVIRONMENT_VARIABLE","value":"eu","writeOnly":false}' through A
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TOKEN","type":"ENVIRONMENT_VARIABLE","value":"s3cret","writeOnly":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"URL","type":"ENVIRONMENT_VARIABLE","value":"https://${{ env.REGION }}.example.com/${{ stack.id }}/${{ run.type }}","writeOnly":false}' through A
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"AUTH","type":"ENVIRONMENT_VARIABLE","value":"Bearer ${{ env.TOKEN }}","writeOnly":false}' through B
    And the launcher "l" registers with the key of "csr" and the certificate "first" through A

  @case:success
  Scenario: A worker's run specification arrives rendered, and a value that reads a secret arrives sealed and write-only
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through A
    And token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then the answer assigns the run "run", its run token kept as "rt"
    When token "rt" sends "GET /worker/v1/runs/{run}/spec" through A
    Then it answers 200
    And the specification's variable "REGION" is "eu"
    And the specification's variable "URL" is "https://eu.example.com/infra/TRACKED"
    And the specification's variable "TF_VAR_infraweave_account_name" is "acme"
    And the specification's variable "TF_VAR_infraweave_commit_branch" is "main"
    And the specification's variable "TF_VAR_infraweave_local_preview" is "false"
    And the specification's variable "TOKEN" is write-only
    And the specification's variable "AUTH" is write-only
    And the answer leaves out "s3cret"
    And the specification's sealed part opens with the key of "csr" to the secret "AUTH" as "Bearer s3cret"
    When token "rt" sends "GET /worker/v1/runs/{run}/spec" through B
    Then the specification's sealed part opens with the key of "csr" to the secret "TOKEN" as "s3cret"

  @case:denial
  Scenario: A run token reads only its run's specification, and a worker's session token reads none
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through B
    And token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through A
    Then the answer assigns the run "run", its run token kept as "rt"
    When token "l" sends "GET /worker/v1/runs/{run}/spec" through B
    Then it answers a 401 problem "unauthenticated"
    When token "rt" sends "POST /worker/v1/poll" with '{"busy":true}' through A
    Then it answers a 401 problem "unauthenticated"
    When token "rt" sends "GET /worker/v1/runs/01KACME000000000000000000A/spec" through B
    Then it answers a 401 problem "unauthenticated"
    When token "h" sends "GET /worker/v1/runs/{run}/spec" through A
    Then it answers a 401 problem "unauthenticated"
