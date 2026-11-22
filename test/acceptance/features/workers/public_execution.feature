@postgres @objectstore @vcs @docker
Feature: Installation public workers execute tenant runs
  A real launcher registers with installation credentials and executes a
  provider-free Terraform configuration using the pinned runner's baked CLI.
  The operator key never authorizes tenant or worker protocol requests.

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database, with an operator key and the settings '{"worker":{"public_pool":{"enabled":true}}}'
    And the operator key is exchanged through A for token "op"
    And the home key is exchanged through B for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And token "op" sends "POST /operator/v1/public-worker-pool" with '{"certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "public"
    And the answer's pool token names the pool "public" and chains to the pool CA, its serial kept as "publiccert"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = "public-worker"\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "op" starts the public launcher "l" of the pool "public" with the key of "csr" through A

  @op:runTrigger @case:success
  Scenario: A public launcher plans applies and finishes on the requested account
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","autodeploy":true,"skipVendorBinaryDownload":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And the relay holds the runners' reports
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then the relay holds a report of the run "run"
    And the run "run"'s container started from the image "{runner.terraform}"
    When the relay releases the runners' reports
    Then token "h" eventually reads the run "run" in the state "FINISHED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, APPLYING, FINISHED"
    And token "h" reads the run "run"'s APPLYING log through B, which holds "Apply complete! Resources: 1 added, 0 changed, 0 destroyed." and not "Error"
    And the run "run" wrote 1 state version

  @op:runTrigger @case:denial
  Scenario: A public launcher refuses the resolved unlisted image before creating a container
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","autodeploy":true,"runnerImage":"unlisted.example/runner:1","skipVendorBinaryDownload":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note is "runner image is not admitted by the installation public pool"

  @op:runTrigger @op:workerPoll @case:success
  Scenario: Public execution respects the Free slot ceiling and resumes waiting work after release
    Given token "op" gives the home organization the tier "Free" through A
    And token "op" turns billing on through A
    And the public launcher "probe" registers with the key of "csr" and the certificate "publiccert" through B
    And it answers 200
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","autodeploy":true,"skipVendorBinaryDownload":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Other","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","autodeploy":true,"skipVendorBinaryDownload":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And the relay holds the runners' reports
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "firstRun"
    Then the relay holds a report of the run "firstRun"
    When token "h" sends "POST /api/v1/stacks/other/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "secondRun"
    Then token "h" eventually reads the run "secondRun" in the state "READY" through A
    When token "probe" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then it answers 200
    And the answer has no worker assignment
    And token "h" eventually reads the run "secondRun" in the state "READY" through A
    When the relay releases the runners' reports
    Then token "h" eventually reads the run "firstRun" in the state "FINISHED" through A
    And token "h" eventually reads the run "secondRun" in the state "FINISHED" through B
    And the run "firstRun" wrote 1 state version
    And the run "secondRun" wrote 1 state version
