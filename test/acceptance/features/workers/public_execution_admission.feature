@postgres @objectstore @vcs @docker
Feature: Public launchers enforce admission and release shared capacity
  Registry checks apply to the final resolved image before container creation.
  Stopping a run or losing its worker releases the shared organization slot
  so waiting work can execute through another application replica.

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database, with an operator key and the settings '{"worker":{"public_pool":{"enabled":true},"poll_timeout_max":"5s","offline_after":"8s"},"run":{"lost_after":"45s"}}'
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

  @op:runTrigger @case:denial
  Scenario Outline: A public launcher refuses registry lookalikes and unadmitted ports
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","runnerImage":"<image>","skipVendorBinaryDownload":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, FAILED"
    And the answer's last state transition's note is "runner image is not admitted by the installation public pool"
    And the relay passed "GET /worker/v1/runs/{run}/spec" answered 200
    And the run "run" never started a container

    Examples:
      | image                                  |
      | ghcr.io.evil.invalid/weave/runner:1      |
      | evilghcr.io/weave/runner:1              |
      | ghcr.io:444/weave/runner:1              |

  @op:runStop @op:workerPoll @case:success
  Scenario: Stopping a real public plan admits waiting work after its terminal report
    Given token "op" gives the home organization the tier "Free" through A
    And token "op" turns billing on through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","skipVendorBinaryDownload":true,"vendorConfig":{"terraform":{}},"beforePlan":["echo holding public capacity","sleep 600"]},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Other","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","autodeploy":true,"skipVendorBinaryDownload":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "firstRun"
    Then token "h" eventually reads the run "firstRun" in the state "PLANNING" through A
    When token "h" sends "POST /api/v1/stacks/other/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "secondRun"
    Then token "h" eventually reads the run "secondRun" in the state "READY" through A
    When token "h" sends "POST /api/v1/stacks/infra/runs/{firstRun}/stop" with '{"note":"release public capacity","state":"PLANNING"}' through A
    Then it answers 200
    And token "h" eventually reads the run "firstRun" in the state "STOPPED" through B
    And token "h" eventually reads the run "secondRun" in the state "FINISHED" through A
    And the run "secondRun" wrote 1 state version

  @op:workerPoll @case:failure
  Scenario: Losing a real public launcher releases waiting work for a replacement
    Given token "op" gives the home organization the tier "Free" through A
    And token "op" turns billing on through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","autodeploy":true,"skipVendorBinaryDownload":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Other","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","autodeploy":true,"skipVendorBinaryDownload":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And the relay holds the runners' reports
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "firstRun"
    Then the relay holds a report of the run "firstRun"
    When the launcher "l" is killed
    And token "h" sends "POST /api/v1/stacks/other/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "secondRun"
    Then token "h" eventually reads the run "secondRun" in the state "READY" through A
    And token "h" eventually reads the run "firstRun" in the state "FAILED" through B
    And the answer's last state transition's note is "worker lost"
    When the relay releases the runners' reports
    And token "op" starts the public launcher "replacement" of the pool "public" with the key of "csr" through B
    Then token "h" eventually reads the run "secondRun" in the state "FINISHED" through A
    And the run "secondRun" wrote 1 state version
