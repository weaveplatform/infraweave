@postgres @vcs
Feature: A run's specification names its source, tool and masking
  A worker of the run's private pool fetches the run's specification with
  its run token. The clear part names where the worker fetches the run's
  source by Git, and whether its credential reads that repository alone;
  a raw Git repository needs no credential, so the sealed part carries
  none. It names the tool the launcher mounts: terraform at the newest
  release the version catalogue lists that the stack's constraint allows,
  or the newest when the stack sets none, downloaded unless the stack
  runs its image's own binary; and whether the runner masks well-known
  secrets. A constraint no listed release meets refuses the specification
  with 422 naming the constraint, which the launcher fails the run with.
  The release indexes serve the recorded fixtures. The home key is
  space-admin on the account's top space. Stacks live in legacy and run on
  the pool.
  ref: decision 0008
  ref: decision 0012

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
    And the launcher "l" registers with the key of "csr" and the certificate "cert" through A

  @case:success
  Scenario: A raw Git stack's run fetches its source with no credential and mounts the newest listed Terraform
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}"},"manageState":false}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through A
    And token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then the answer assigns the run "run", its run token kept as "rt"
    When token "rt" sends "GET /worker/v1/runs/{run}/spec" through A
    Then it answers 200
    And the specification fetches its source by Git from "{infra.url}", not narrowed
    And the specification mounts terraform "{terraform.newest}", downloaded, and does not mask well-known secrets
    And the specification's sealed part opens with the key of "csr" to no source credential

  @case:success
  Scenario: A stack's version constraint, masking and image binary reach its run's specification
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","terraformVersion":"~> 1.5.0","enableWellKnownSecretMasking":true,"skipVendorBinaryDownload":true},"manageState":false}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through B
    And token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through A
    Then the answer assigns the run "run", its run token kept as "rt"
    When token "rt" sends "GET /worker/v1/runs/{run}/spec" through B
    Then it answers 200
    And the specification mounts terraform "1.5.7", not downloaded, and masks well-known secrets

  @case:failure
  Scenario: A constraint no listed release meets refuses the specification, naming the constraint
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","terraformVersion":"~> 0.1.0"},"manageState":false}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through A
    And token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then the answer assigns the run "run", its run token kept as "rt"
    When token "rt" sends "GET /worker/v1/runs/{run}/spec" through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/terraformVersion" as "rule"
