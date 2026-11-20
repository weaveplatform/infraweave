@postgres @objectstore @vcs @docker @terraform @policy-trigger-worker
Feature: Trigger priority and independent run ownership follow actual Rego decisions
  Separate terminal source runs may retry the same target commit. Each source
  creates its own child once; mixed Rego dialects combine deterministically,
  and the child observes null causal ancestry and no inherited source flags.
  ref: decision 0009

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
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
  Scenario: Conflicting V0 and V1 priorities are normal with durable errors and isolated children
    Given token "h" sends "GET /api/v1/stacks/infra" through A
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '{"name":"High legacy","type":"TRIGGER","space":"legacy","engineType":"REGO_V0","body":"package infraweave\ntrigger[\"target\"] { input.run.state == \"CANCELED\"; some n; s := input.stacks[n]; s.id == \"target\"; s.queued_runs <= 1 }\npriority := \"high\"\nflag[\"source-legacy\"] { true }\n"}' through A
    And it answers 201
    And the answer's id names "legacy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{legacy}" through B
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '{"name":"Low modern","type":"TRIGGER","space":"legacy","engineType":"REGO_V1","body":"package infraweave\ntrigger contains \"target\" if input.run.state == \"CANCELED\"\npriority := \"low\"\nflag contains \"source-modern\" if true\n"}' through A
    And it answers 201
    And the answer's id names "modern"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{modern}" through B
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '{"name":"Independent child","type":"TRIGGER","space":"legacy","engineType":"REGO_V1","body":"package infraweave\nflag contains \"child-null\" if { input.run.state == \"FINISHED\"; input.run.trigger == null; count(input.run.flags) == 0; startswith(input.run.triggered_by, \"policy/infra/\"); input.run.priority == \"normal\"; input.run.commit.hash == \"{targetSeed}\" }\n"}' through A
    And it answers 201
    And the answer's id names "childPolicy"
    And token "h" sends "PUT /api/v1/stacks/target/policies/{childPolicy}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{sourceSeed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "source"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{source}/cancel" with '{}' through B
    Then it answers 200
    And token "h" eventually reads TRIGGER outcomes "TRIGGER, TRIGGER" for run "source" through A
    And the answer contains a TRIGGER error receipt "PRIORITY_CONFLICT"
    And the answer's flags is '["source-legacy","source-modern"]'
    And token "h" eventually reads the single policy-triggered run of stack "target" from stack "infra" run "source" as "child" through B
    When token "h" sends "GET /api/v1/runs/{child}" through A
    Then it answers 200
    And the answer's priorityPreset is '"normal"'
    And the answer's flags is '[]'
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{sourceSeed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "secondSource"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{secondSource}/cancel" with '{}' through B
    Then it answers 200
    And token "h" eventually reads TRIGGER outcomes "TRIGGER, TRIGGER" for run "secondSource" through A
    And the answer contains a TRIGGER error receipt "PRIORITY_CONFLICT"
    And the answer's flags is '["source-legacy","source-modern"]'
    And token "h" eventually reads the single policy-triggered run of stack "target" from stack "infra" run "secondSource" as "secondChild" through B
    When token "h" sends "GET /api/v1/runs/{secondChild}" through A
    Then it answers 200
    And the answer's priorityPreset is '"normal"'
    And the answer's flags is '[]'
    When token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    Then token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B
    And token "h" eventually reads the run "child" in the state "FINISHED" through B
    And token "h" eventually reads TRIGGER outcomes "IGNORE" for run "child" through A
    And the answer's flags is '["child-null"]'
    And the answer's commit hash is "{targetSeed}"
    And the answer's triggeredBy is '"policy/infra/{source}"'
    And token "h" eventually reads the single policy-triggered run of stack "target" from stack "infra" run "source" as "child" through B
    And token "h" eventually reads the run "secondChild" in the state "FINISHED" through B
    And token "h" eventually reads TRIGGER outcomes "IGNORE" for run "secondChild" through A
    And the answer's flags is '["child-null"]'
    And the answer's commit hash is "{targetSeed}"
    And the answer's triggeredBy is '"policy/infra/{secondSource}"'
    And token "h" eventually reads the single policy-triggered run of stack "target" from stack "infra" run "secondSource" as "secondChild" through B
