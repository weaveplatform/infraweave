@postgres @objectstore @vcs @docker @terraform @policy-trigger-worker
Feature: Trigger failures are durable and never leave partial downstream runs
  Actual OPA evaluates terminal sources through two production servers. Policy
  failures and refused targets leave readable source evidence and no child.
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


  @case:failure
  Scenario: An invalid priority refuses every target
    Given token "h" sends "GET /api/v1/stacks/target" through A
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '{"name":"An invalid priority refuses every target","type":"TRIGGER","space":"legacy","engineType":"REGO_V1","body":"package infraweave\ntrigger contains \"target\" if true\npriority := \"urgent\"\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{sourceSeed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "source"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{source}/cancel" with '{}' through B
    Then it answers 200
    And token "h" eventually reads TRIGGER outcomes "IGNORE" for run "source" through A
    And the answer contains a TRIGGER error receipt "RESULT"
    And the answer's state is '"CANCELED"'
    When token "h" sends "GET /api/v1/stacks/target/runs" through B
    Then it answers 200
    And the answer's items is '[]'

  @case:failure
  Scenario: An actual OPA execution conflict creates no target
    Given token "h" sends "GET /api/v1/stacks/target" through A
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '{"name":"An actual OPA execution conflict creates no target","type":"TRIGGER","space":"legacy","engineType":"REGO_V1","body":"package infraweave\ntrigger contains \"target\" if true\npriority := \"high\" if input.run.state == \"CANCELED\"\npriority := \"low\" if input.run.type == \"TRACKED\"\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{sourceSeed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "source"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{source}/cancel" with '{}' through B
    Then it answers 200
    And token "h" eventually reads TRIGGER outcomes "IGNORE" for run "source" through A
    And the answer contains a TRIGGER error receipt "EXECUTION"
    And the answer's state is '"CANCELED"'
    When token "h" sends "GET /api/v1/stacks/target/runs" through B
    Then it answers 200
    And the answer's items is '[]'

  @case:failure
  Scenario: A malformed target collection creates no target
    Given token "h" sends "GET /api/v1/stacks/target" through A
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '{"name":"A malformed target collection creates no target","type":"TRIGGER","space":"legacy","engineType":"REGO_V1","body":"package infraweave\ntrigger contains 42 if true\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{sourceSeed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "source"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{source}/cancel" with '{}' through B
    Then it answers 200
    And token "h" eventually reads TRIGGER outcomes "IGNORE" for run "source" through A
    And the answer contains a TRIGGER error receipt "RESULT"
    And the answer's state is '"CANCELED"'
    When token "h" sends "GET /api/v1/stacks/target/runs" through B
    Then it answers 200
    And the answer's items is '[]'

  @case:failure
  Scenario: A target refused by disable leaves durable source evidence
    Given token "h" sends "POST /api/v1/stacks/target/disable" through A
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '{"name":"Refused target","type":"TRIGGER","space":"legacy","engineType":"REGO_V1","body":"package infraweave\ntrigger contains \"target\" if true\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{sourceSeed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "source"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{source}/cancel" with '{}' through B
    Then it answers 200
    And token "h" eventually reads TRIGGER outcomes "IGNORE" for run "source" through A
    And the answer contains a TRIGGER error receipt "TARGET"
    And the answer's state is '"CANCELED"'
    When token "h" sends "GET /api/v1/stacks/target/runs" through B
    Then it answers 200
    And the answer's items is '[]'

  @case:failure
  Scenario: A target refused by lock leaves durable source evidence
    Given token "h" sends "POST /api/v1/stacks/target/lock" through A
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '{"name":"Refused target","type":"TRIGGER","space":"legacy","engineType":"REGO_V1","body":"package infraweave\ntrigger contains \"target\" if true\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{sourceSeed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "source"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{source}/cancel" with '{}' through B
    Then it answers 200
    And token "h" eventually reads TRIGGER outcomes "IGNORE" for run "source" through A
    And the answer contains a TRIGGER error receipt "TARGET"
    And the answer's state is '"CANCELED"'
    When token "h" sends "GET /api/v1/stacks/target/runs" through B
    Then it answers 200
    And the answer's items is '[]'
