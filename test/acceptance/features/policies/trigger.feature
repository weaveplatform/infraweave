@postgres @objectstore @vcs @docker @terraform @policy-trigger-worker
Feature: Terminal runs durably trigger target runs on their own tracked commits
  Actual workers finish a source and its downstream run. The policy consumes
  committed source flags and current target metadata. A target uses its own
  tracked commit and receives no flags from its source.
  ref: decision 0009
  ref: decision 0011

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "source" {\n  input = "source-653"\n}\n |
    And a Gitea repository "downstream" with the files:
      | main.tf | resource "terraform_data" "target" {\n  input = "target-653"\n}\n |
    And the head of "main" of "infra" is kept as "sourceSeed"
    And the head of "main" of "downstream" is kept as "targetSeed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through A
    And it answers 201
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Target","repository":"downstream","branch":"main","provider":"GIT","repositoryURL":"{downstream.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "sourceSeed" through A
    And token "h" eventually reads the stack "target" tracking the commit kept as "targetSeed" through B

  @case:success
  Scenario: A finished tracked source evaluates TRIGGER and creates one isolated downstream run
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Mark source plan","type":"PLAN","space":"legacy","body":"package infraweave\nflag contains \"source-plan\" if true\n"}' through A
    And it answers 201
    And the answer's id names "planPolicy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{planPolicy}" through B
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '{"name":"Run downstream on its own commit","type":"TRIGGER","space":"legacy","body":"package infraweave\ntrigger contains \"target\" if { input.run.state == \"FINISHED\"; input.run.type == \"TRACKED\"; \"source-plan\" in input.run.flags; some s in input.stacks; s.id == \"target\"; s.queued_runs == 0; s.tracked_commit.hash == \"{targetSeed}\" }\nflag contains \"source-trigger\" if { count(trigger) > 0 }\n"}' through A
    And it answers 201
    And the answer's id names "triggerPolicy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{triggerPolicy}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{sourceSeed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "source"
    And token "h" eventually reads the run "source" in the state "FINISHED" through B
    And token "h" eventually reads the single policy-triggered run of stack "target" from stack "infra" run "source" as "child" through A
    And token "h" eventually reads the run "child" in the state "FINISHED" through B
    And the answer's commit hash is "{targetSeed}"
    And the answer's type is '"TRACKED"'
    And the answer's triggeredBy is '"policy/infra/{source}"'
    And the answer's flags is '[]'
    When token "h" sends "GET /api/v1/runs/{source}" through A
    Then it answers 200
    And the answer's flags is '["source-plan","source-trigger"]'
    And the answer's TRIGGER receipt outcomes are "TRIGGER"
