@postgres @objectstore @vcs @docker @terraform
Feature: A stack runs unchanged in Spacelift's public Terraform runner image
  Spacelift's public runner-terraform image meets the runner image
  contract: a user of UID 1983, sh, and a workspace at /mnt/workspace that
  user owns. A stack that names it as its runner image runs in it as in
  infraweave's own: a real launcher of a private pool pulls the image,
  pinned by digest, and mounts this build's runner and the newest
  Terraform the version catalogue lists, which it downloads from the
  suite's tool mirror and verifies against HashiCorp's signing key. A
  tracked run on an autodeploy stack plans, applies and finishes. The
  release indexes serve the recorded fixtures; the home key is
  space-admin on the account's top space. Stack infra lives in legacy,
  keeps its state in infraweave and runs on the pool; the repository's
  configuration has a terraform_data resource, so no provider is
  downloaded.
  ref: decision 0008
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = "one"\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  @op:runTrigger @case:success
  Scenario: A tracked run on an autodeploy stack in Spacelift's public runner image applies and finishes
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"runnerImage":"{runner.spacelift}","vendorConfig":{"terraform":{}},"beforeInit":["echo \"user: $(id -u)\""]},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FINISHED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, APPLYING, FINISHED"
    And token "h" reads the run "run"'s INITIALIZING log through B, which holds "user: 1983" and not "user: 0"
    And token "h" reads the run "run"'s APPLYING log through A, which holds "Apply complete! Resources: 1 added, 0 changed, 0 destroyed." and not "Error"
    And the run "run" wrote 1 state version
