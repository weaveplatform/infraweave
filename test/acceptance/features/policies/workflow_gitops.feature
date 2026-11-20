@policy-phase @terraform-vcs @postgres @objectstore @github @gitlab @bitbucket-cloud @bitbucket-dc @azure-devops @docker @terraform @policy-push @policy-push-workers @policy-plan-worker
Feature: GitOps policy decisions reach real workers and native feedback
  Signed provider requests and their branch pushes create one proposed run.
  Actual Terraform uploads are evaluated by the linked OPA runtime before
  durable jobs deliver a native check and exactly one labelled plan comment.
  These native HTTP fixtures are distinct from POL-18's upstream vcs-lab proof.
  ref: decision 0009
  ref: decision 0012
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"acme","certificateSigningRequest":"{csr}"}' through A
    And it answers 201
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  @case:success
  Scenario Outline: A signed <provider> proposal evaluates its real plan and publishes one labelled comment
    Given a "<provider>" plan-comment stack "infra" using "terraform" with labels '["feature:add_plan_pr_comment"]'
    And token "h" sends "POST /api/v1/policies" with '{"name":"Native request","type":"GIT_PUSH","body":"package infraweave\npropose if { input.push == null; input.pull_request.action == \"opened\"; input.pull_request.head.branch == \"feature\"; input.pull_request.base.branch == \"target-12\" }\npropose if input.push.branch == \"feature\"\nflag contains \"pushed\"\n"}' through A
    And it answers 201
    And the answer's id names "pushPolicy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{pushPolicy}" through B
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '{"name":"Review external proposal","type":"PLAN","body":"package infraweave\nchanges := {change.address | some change in input.terraform.resource_changes; change.change.after.input == sanitized(\"one\")}\nvalid if { changes == {\"terraform_data.this\"}; count(input.terraform.terraform_version) > 0 }\nwarn contains \"Review real proposed plan\" if valid\ndeny contains \"Missing real tool document\" if { not valid }\nflag contains \"planned\" if valid\n"}' through A
    And it answers 201
    And the answer's id names "planPolicy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{planPolicy}" through B
    And it answers 200
    When the event fixture sends request "request" through A
    Then event delivery "request" settles with 1 new runs
    And the "PROPOSED" event run on stack "infra" for commit "seed" is kept as "run"
    When the event fixture pushes "branch" from "commentBase" to "seed" on "feature" with "valid" verification through B and receives 202
    Then event delivery "branch" settles with 0 new runs
    When the event fixture pushes "branch" from "commentBase" to "seed" on "feature" with "valid" verification through A and receives 200
    Then event delivery "branch" settles with 0 new runs
    And event delivery "branch" has exactly 1 stored rows
    And token "h" eventually reads the run "run" in the state "FINISHED" through B
    And the answer's flags is '["planned","pushed"]'
    And the answer's plan policy outcomes are '[{"deny":[],"warn":["Review real proposed plan"]}]'
    And the answer's PLAN receipt outcomes are "ALLOW"
    And the committed workflow update of run "run" matches the public policy snapshot
    And stack "infra" has exactly 1 event runs for commit "seed"
    And comment events for run "run" have settled
    And the native workflow feedback for run "run" on request 12 has terminal check state "<state>"

    Examples:
      | provider        | state      |
      | GitHub          | success    |
      | GitLab          | success    |
      | Bitbucket Cloud | SUCCESSFUL |
      | Bitbucket DC    | SUCCESSFUL |
      | Azure DevOps    | succeeded  |
