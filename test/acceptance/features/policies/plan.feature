@postgres @objectstore @vcs @docker @policy-plan-worker
Feature: Plan policies select the next state of real Terraform family workers
  The worker reports its recorded sanitized plan while the run remains PLANNING.
  Denial commits evidence and prevents apply; warnings route to human review.
  ref: decision 0008
  ref: decision 0009

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And token "h" eventually reads the OpenTofu versions through B as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = "approval-policy"\n} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  @terraform @case:failure
  Scenario: A real sanitized Terraform plan is denied before any apply
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And token "h" sends "POST /api/v1/policies" with '{"name":"Deny actual Terraform resource","type":"PLAN","space":"legacy","body":"package infraweave\ndeny contains \"terraform_data requires review\" if { input.infraweave.run.state == \"PLANNING\"; input.terraform.resource_changes[0].type == \"terraform_data\" }\nflag contains \"plan-denied\" if { count(deny) > 0 }\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "FAILED" through B
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, FAILED"
    And the answer's last state transition's note is "denied by plan policy"
    And the answer's flags is '["plan-denied"]'
    And the answer's plan policy outcomes are '[{"deny":["terraform_data requires review"],"warn":[]}]'
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through A

  @terraform @op:runReview @case:success
  Scenario: A proposed plan warning waits for a state-scoped review that reads the plan flag
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And token "h" sends "POST /api/v1/policies" with '{"name":"Review actual proposed changes","type":"PLAN","space":"legacy","body":"package infraweave\nwarn contains \"Review proposed resources\" if { count(input.terraform.resource_changes) > 0 }\nflag contains \"plan-warned\" if { count(warn) > 0 }\n"}' through A
    And it answers 201
    And the answer's id names "planPolicy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{planPolicy}" through B
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '{"name":"Review warning in its own state","type":"APPROVAL","space":"legacy","body":"package infraweave\napprove if { input.run.state == \"QUEUED\" }\napprove if { input.run.state == \"PENDING_REVIEW\"; count(input.reviews.current.approvals) == 1; \"plan-warned\" in input.run.flags }\nflag contains \"pending-approved\" if { approve; input.run.state == \"PENDING_REVIEW\" }\n"}' through A
    And it answers 201
    And the answer's id names "approvalPolicy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{approvalPolicy}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"PROPOSED"}' through A
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "PENDING_REVIEW" through B
    And the answer's needsApproval is 'true'
    And the answer's canConfirm is 'false'
    And the answer's flags is '["plan-warned"]'
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through A
    When token "h" sends "POST /api/v1/stacks/infra/runs/review" with '{"run":"{run}","decision":"APPROVE","stateVersion":6}' through B
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FINISHED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, PENDING_REVIEW, FINISHED"
    And the answer's flags is '["pending-approved","plan-warned"]'

  @terraform @op:runReview @case:failure
  Scenario: One rejection among approving policies fails an actual proposed warning
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And token "h" sends "POST /api/v1/policies" with '{"name":"Review actual proposed changes","type":"PLAN","space":"legacy","body":"package infraweave\nwarn contains \"Review proposed resources\" if { count(input.terraform.resource_changes) > 0 }\nflag contains \"plan-warned\" if { count(warn) > 0 }\n"}' through A
    And it answers 201
    And the answer's id names "planPolicy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{planPolicy}" through B
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '{"name":"Review warning in its own state","type":"APPROVAL","space":"legacy","body":"package infraweave\napprove if { input.run.state == \"QUEUED\" }\napprove if { input.run.state == \"PENDING_REVIEW\"; count(input.reviews.current.approvals) == 1; \"plan-warned\" in input.run.flags }\nreject if { input.run.state == \"PENDING_REVIEW\"; count(input.reviews.current.rejections) == 1 }\nflag contains \"pending-rejected\" if reject\n"}' through A
    And it answers 201
    And the answer's id names "approvalPolicy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{approvalPolicy}" through B
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '{"name":"Another approving policy","type":"APPROVAL","space":"legacy","body":"package infraweave\napprove if true\nflag contains \"other-approved\" if { input.run.state == \"PENDING_REVIEW\" }\n"}' through A
    And it answers 201
    And the answer's id names "secondApproval"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{secondApproval}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}","runType":"PROPOSED"}' through A
    Then it answers 200
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "PENDING_REVIEW" through B
    And the answer's needsApproval is 'true'
    And the answer's canConfirm is 'false'
    And the answer's flags is '["other-approved","plan-warned"]'
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through A
    When token "h" sends "POST /api/v1/stacks/infra/runs/review" with '{"run":"{run}","decision":"REJECT","stateVersion":6}' through B
    Then it answers 200
    And token "h" eventually reads the run "run" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, PENDING_REVIEW, FAILED"
    And the answer's flags is '["other-approved","pending-rejected","plan-warned"]'
    And the answer's last state transition's note is "Run rejected by approval policies"
