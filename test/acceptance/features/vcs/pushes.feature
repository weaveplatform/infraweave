@postgres @objectstore @gitlab @docker
Feature: Default VCS push runs
  Verified GitLab deliveries prepare one complete diff per ref, then evaluate
  each affected stack under its own queue fence. Real launchers plan with the
  pinned Terraform and OpenTofu catalogue tools. No pull request is required.
  ref: decision 0012
  ref: decision 0016
  ref: report 11

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And token "h" eventually reads the OpenTofu versions through B as the fixture's stable releases newest first
    And a GitLab user "dev" with a token kept as "tok"
    And a GitLab project "acme/platform/infra" that "dev" maintains, its head of "main" kept as "initial"
    And the GitLab project "acme/platform/infra" commits files on "main" as "seed":
      | root/main.tf | resource "terraform_data" "this" {\n  input = "base"\n}\n |
      | outside.txt | base |
    And token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"{tok}","customInput":{"name":"Pushes","spaceID":"acme","isDefault":true}}' through A
    And it answers 201
    And the answer's id names "integration"
    And the GitLab webhook secret is kept privately
    And a certificate signing request for a P-256 key is kept as "csr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"acme","certificateSigningRequest":"{csr}"}' through A
    And it answers 201
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  Scenario Outline: Tracked and proposed pushes plan once, while outside changes and tags create no run
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","namespace":"acme/platform","repository":"infra","branch":"main","provider":"GITLAB","space":"acme","workerPool":"{pool}","projectRoot":"root","vendorConfig":{"<vendor>":{}},"autodeploy":true},"manageState":true}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    When the GitLab project "acme/platform/infra" commits files on "main" as "tracked":
      | root/main.tf | resource "terraform_data" "this" {\n  input = "one"\n}\n |
    And GitLab pushes "trackedPush" for integration "integration" from "seed" to "tracked" on "main" through A
    Then it answers 202
    And GitLab push "trackedPush" for integration "integration" settles with 1 runs and 1 stack jobs
    And the "TRACKED" event run on stack "infra" for commit "tracked" is kept as "trackedRun"
    And token "h" eventually reads the run "trackedRun" in the state "FINISHED" through B
    And the answer's triggeredBy is '"vcs/commit"'
    And the event run "trackedRun" has a real tool plan through B
    And token "h" sends "GET /api/v1/stacks/infra" through A
    And the answer tracks the commit kept as "tracked" at the branch head kept as "tracked"
    When GitLab explicitly resends delivery "trackedPush" for integration "integration" through B
    Then it answers 200
    When GitLab pushes "sameCommit" for integration "integration" from "seed" to "tracked" on "main" through B
    Then it answers 202
    And GitLab push "sameCommit" for integration "integration" settles with 0 runs and 1 stack jobs
    And stack "infra" has exactly 1 event runs for commit "tracked"
    When the GitLab project "acme/platform/infra" commits files on "main" as "outside":
      | outside.txt | outside-only |
    And GitLab pushes "outsidePush" for integration "integration" from "tracked" to "outside" on "main" through A
    Then it answers 202
    And GitLab push "outsidePush" for integration "integration" settles with 0 runs and 1 stack jobs
    And token "h" sends "GET /api/v1/stacks/infra" through B
    And the answer tracks the commit kept as "tracked" at the branch head kept as "outside"
    When the GitLab project "acme/platform/infra" branches "feature" from "outside"
    And the GitLab project "acme/platform/infra" commits files on "feature" as "proposed":
      | root/main.tf | resource "terraform_data" "this" {\n  input = "proposal"\n}\n |
    And GitLab pushes "proposedPush" for integration "integration" from "outside" to "proposed" on "feature" through B
    Then it answers 202
    And GitLab push "proposedPush" for integration "integration" settles with 1 runs and 1 stack jobs
    And the "PROPOSED" event run on stack "infra" for commit "proposed" is kept as "proposedRun"
    And token "h" eventually reads the run "proposedRun" in the state "FINISHED" through A
    And the event run "proposedRun" has a real tool plan through A
    When the GitLab project "acme/platform/infra" branches "main" from "proposed"
    And GitLab pushes "fastForward" for integration "integration" from "outside" to "proposed" on "main" through A
    Then it answers 202
    And GitLab push "fastForward" for integration "integration" settles with 1 runs and 1 stack jobs
    And the "TRACKED" event run on stack "infra" for commit "proposed" is kept as "fastForwardRun"
    And token "h" eventually reads the run "fastForwardRun" in the state "FINISHED" through B
    And stack "infra" has exactly 2 event runs for commit "proposed"
    When GitLab sends a "merge-request" delivery "request" for integration "integration" with "current" token through B
    Then it answers 202
    And GitLab push "request" for integration "integration" settles with 0 runs and 1 stack jobs
    And stack "infra" has exactly 2 event runs for commit "proposed"
    When GitLab sends a "tag" delivery "tagPush" for integration "integration" with "current" token through A
    Then it answers 202
    And GitLab push "tagPush" for integration "integration" settles with 0 runs and 1 stack jobs
    And stack "infra" has exactly 0 event runs for commit "seed"
    And no GitLab webhook token reaches retained rows or server logs

    @terraform
    Examples:
      | vendor |
      | terraform |

    @tofu
    Examples:
      | vendor |
      | opentofu |

  Scenario Outline: A new branch uses its default merge base, and a force push retains changes from endpoints and new commits
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","namespace":"acme/platform","repository":"infra","branch":"main","provider":"GITLAB","space":"acme","workerPool":"{pool}","projectRoot":"root","vendorConfig":{"<vendor>":{}}},"manageState":true}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And the GitLab project "acme/platform/infra" commits files on "main" as "advanced":
      | root/main.tf | resource "terraform_data" "this" {\n  input = "default-advanced"\n}\n |
    And the GitLab project "acme/platform/infra" branches "diverged" from "seed"
    And the GitLab project "acme/platform/infra" commits files on "diverged" as "head":
      | outside.txt | feature-only |
    When GitLab pushes "newBranch" for integration "integration" from "zero" to "head" on "diverged" through A
    Then it answers 202
    And GitLab push "newBranch" for integration "integration" settles with 0 runs and 1 stack jobs
    And stack "infra" has exactly 0 event runs for commit "head"
    When GitLab pushes "forced" for integration "integration" from "advanced" to "head" on "diverged" through B
    Then it answers 202
    And GitLab push "forced" for integration "integration" settles with 1 runs and 1 stack jobs
    And the "PROPOSED" event run on stack "infra" for commit "head" is kept as "forcedRun"
    And token "h" eventually reads the run "forcedRun" in the state "FINISHED" through A
    And the event run "forcedRun" has a real tool plan through B
    When GitLab pushes "deleted" for integration "integration" from "head" to "zero" on "diverged" through B
    Then it answers 202
    And GitLab push "deleted" for integration "integration" settles with 0 runs and 0 stack jobs
    And stack "infra" has exactly 1 event runs for commit "head"
    When the GitLab project "acme/platform/infra" branches "same-tree" from "seed"
    And the GitLab project "acme/platform/infra" commits files on "same-tree" as "same":
      | root/main.tf | resource "terraform_data" "this" {\n  input = "default-advanced"\n}\n |
    And GitLab pushes "unchangedTrees" for integration "integration" from "advanced" to "same" on "same-tree" through A
    Then it answers 202
    And GitLab push "unchangedTrees" for integration "integration" settles with 1 runs and 1 stack jobs
    And stack "infra" has exactly 1 event runs for commit "same"
    And the "PROPOSED" event run on stack "infra" for commit "same" is kept as "equalTreeRun"
    And token "h" eventually reads the run "equalTreeRun" in the state "FINISHED" through B
    And the event run "equalTreeRun" has a real tool plan through A

    @terraform
    Examples:
      | vendor |
      | terraform |

    @tofu
    Examples:
      | vendor |
      | opentofu |

  @terraform
  Scenario: A newer push stops an older real planning proposal on the same branch
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","namespace":"acme/platform","repository":"infra","branch":"main","provider":"GITLAB","space":"acme","workerPool":"{pool}","projectRoot":"root","vendorConfig":{"terraform":{}},"beforePlan":["echo old-planning; sleep 300"]},"manageState":true}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And the GitLab project "acme/platform/infra" branches "feature" from "seed"
    And the GitLab project "acme/platform/infra" commits files on "feature" as "old":
      | root/main.tf | resource "terraform_data" "this" {\n  input = "old"\n}\n |
    When GitLab pushes "oldPush" for integration "integration" from "seed" to "old" on "feature" through A
    Then it answers 202
    And GitLab push "oldPush" for integration "integration" settles with 1 runs and 1 stack jobs
    And the "PROPOSED" event run on stack "infra" for commit "old" is kept as "oldRun"
    And token "h" eventually reads the run "oldRun" in the state "PLANNING" through B
    And token "h" sends "PATCH /api/v1/stacks/infra" with '{"beforePlan":["echo new-planning"]}' through A
    And it answers 200
    When the GitLab project "acme/platform/infra" commits files on "feature" as "new":
      | root/main.tf | resource "terraform_data" "this" {\n  input = "new"\n}\n |
    And GitLab pushes "newPush" for integration "integration" from "old" to "new" on "feature" through B
    Then it answers 202
    And GitLab push "newPush" for integration "integration" settles with 1 runs and 1 stack jobs
    And token "h" eventually reads the run "oldRun" in the state "STOPPED" through A
    And the answer's last state transition's note is "superseded by commit {new}"
    And the "PROPOSED" event run on stack "infra" for commit "new" is kept as "newRun"
    And token "h" eventually reads the run "newRun" in the state "FINISHED" through B
    And the event run "newRun" has a real tool plan through A

  @terraform
  Scenario: Parallel stack children share one delivery cap and retain the admitted run
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Alpha","namespace":"acme/platform","repository":"infra","branch":"main","provider":"GITLAB","space":"acme","workerPool":"{pool}","projectRoot":"root","vendorConfig":{"terraform":{}}},"manageState":true}' through A
    And it answers 201
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Beta","namespace":"acme/platform","repository":"infra","branch":"main","provider":"GITLAB","space":"acme","workerPool":"{pool}","projectRoot":"root","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And it answers 201
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Gamma","namespace":"acme/platform","repository":"infra","branch":"main","provider":"GITLAB","space":"acme","workerPool":"{pool}","projectRoot":"root","vendorConfig":{"terraform":{}}},"manageState":true}' through A
    And it answers 201
    And token "h" eventually reads the stack "alpha" tracking the commit kept as "seed" through A
    And token "h" eventually reads the stack "beta" tracking the commit kept as "seed" through B
    And token "h" eventually reads the stack "gamma" tracking the commit kept as "seed" through A
    And token "h" sends "POST /api/v1/account/update-vcs-event-triggered-runs-limit" with '{"limit":2}' through B
    And it answers 200
    And the GitLab project "acme/platform/infra" branches "feature" from "seed"
    And the GitLab project "acme/platform/infra" commits files on "feature" as "limited":
      | root/main.tf | resource "terraform_data" "this" {\n  input = "limited"\n}\n |
    When GitLab pushes "limitedPush" for integration "integration" from "seed" to "limited" on "feature" through A
    Then it answers 202
    And GitLab push "limitedPush" for integration "integration" settles with 2 runs and 3 stack jobs
    And commit "limited" has exactly 2 event runs across all stacks
    And all event runs for commit "limited" finish through B
    And token "h" sends "GET /api/v1/stacks/alpha" through A
    And the answer tracks the commit kept as "seed" at the branch head kept as "seed"
    And token "h" sends "GET /api/v1/stacks/beta" through B
    And the answer tracks the commit kept as "seed" at the branch head kept as "seed"
    And token "h" sends "GET /api/v1/stacks/gamma" through A
    And the answer tracks the commit kept as "seed" at the branch head kept as "seed"
