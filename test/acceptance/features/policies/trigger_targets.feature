@postgres @objectstore @vcs @docker @terraform @policy-trigger-worker
Feature: Terminal trigger targets obey account, space and lifecycle boundaries
  The policy sees its account's inventory while each output target must be
  reachable from that policy's own space. One unavailable selection prevents
  every provisional child and produces durable evidence on the source.
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

  @case:failure
  Scenario: A deleting target refuses fan-out without losing the source receipt
    Given token "h" sends "DELETE /api/v1/stacks/target?destroyResources=true" through A
    And it answers 200
    And the answer's deleting is 'true'
    And token "h" sends "POST /api/v1/policies" with '{"name":"policy","type":"TRIGGER","space":"legacy","body":"package infraweave\ntrigger contains \"target\" if true\n"}' through A
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
    And token "h" reads no policy-triggered run of stack "target" from stack "infra" run "source" through B

  @case:failure
  Scenario: A target without a tracked commit prevents the otherwise valid target too
    Given token "h" sends "GET /api/v1/stacks/infra" through A
    And it answers 200
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Pending","repository":"downstream","branch":"missing-branch","provider":"GIT","repositoryURL":"{downstream.url}","space":"legacy","autodeploy":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And it answers 201
    And token "h" sends "POST /api/v1/policies" with '{"name":"policy","type":"TRIGGER","space":"legacy","body":"package infraweave\ntrigger contains name if { some name in {\"target\", \"pending\"} }\n"}' through A
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
    And token "h" reads no policy-triggered run of stack "target" from stack "infra" run "source" through B
    And token "h" reads no policy-triggered run of stack "pending" from stack "infra" run "source" through A

  @case:failure
  Scenario Outline: An unreachable space is visible in account inventory but cannot be triggered
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Closed","description":"","inheritEntities":<inherit>,"parentSpace":"<parent>"}' through A
    And it answers 201
    And the answer's id is the space "closed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Unreachable","repository":"downstream","branch":"main","provider":"GIT","repositoryURL":"{downstream.url}","space":"{closed}","autodeploy":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And it answers 201
    And token "h" eventually reads the stack "unreachable" tracking the commit kept as "targetSeed" through B
    And token "h" sends "POST /api/v1/policies" with '{"name":"policy","type":"TRIGGER","space":"legacy","body":"package infraweave\ntrigger contains name if { some owner in input.stacks; owner.id == \"unreachable\"; some name in {\"target\", \"unreachable\"} }\n"}' through A
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
    And token "h" reads no policy-triggered run of stack "target" from stack "infra" run "source" through B
    And token "h" reads no policy-triggered run of stack "unreachable" from stack "infra" run "source" through A

    Examples:
      | parent | inherit |
      | acme   | true    |
      | legacy | false   |

  @case:success
  Scenario: A policy can trigger a stack in an inheriting descendant space
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Child","description":"","inheritEntities":true,"parentSpace":"legacy"}' through A
    And it answers 201
    And the answer's id is the space "childSpace"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Reachable","repository":"downstream","branch":"main","provider":"GIT","repositoryURL":"{downstream.url}","space":"{childSpace}","autodeploy":true,"vendorConfig":{"terraform":{}},"workerPool":"{pool}"},"manageState":true}' through B
    And it answers 201
    And token "h" eventually reads the stack "reachable" tracking the commit kept as "targetSeed" through B
    And token "h" sends "POST /api/v1/policies" with '{"name":"policy","type":"TRIGGER","space":"legacy","body":"package infraweave\ntrigger contains \"reachable\" if input.run.state == \"CANCELED\"\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{sourceSeed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "source"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{source}/cancel" with '{}' through B
    Then it answers 200
    And token "h" eventually reads the single policy-triggered run of stack "reachable" from stack "infra" run "source" as "child" through A
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B
    And token "h" eventually reads the run "child" in the state "FINISHED" through A
    And the answer's commit hash is "{targetSeed}"
    And the answer's triggeredBy is '"policy/infra/{source}"'
    And the answer's flags is '[]'

  @case:failure
  Scenario: Another account's stack is absent from input and cannot be a target
    Given the sibling key is exchanged through A for token "s"
    And token "s" sends "POST /api/v1/stacks" with '{"input":{"name":"Foreign","repository":"downstream","branch":"main","provider":"GIT","repositoryURL":"{downstream.url}","space":"legacy","autodeploy":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through B
    And it answers 201
    And token "s" eventually reads the stack "foreign" tracking the commit kept as "targetSeed" through B
    And token "h" sends "POST /api/v1/policies" with '{"name":"policy","type":"TRIGGER","space":"legacy","body":"package infraweave\ntrigger contains \"foreign\" if true\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '{"name":"inventory","type":"TRIGGER","space":"legacy","body":"package infraweave\nflag contains \"account-private\" if { not has_foreign }\nhas_foreign if { some owner in input.stacks; owner.id == \"foreign\" }\n"}' through A
    And it answers 201
    And the answer's id names "inventory"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{inventory}" through B
    And it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{sourceSeed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "source"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{source}/cancel" with '{}' through B
    Then it answers 200
    And token "h" eventually reads TRIGGER outcomes "IGNORE, IGNORE" for run "source" through A
    And the answer contains a TRIGGER error receipt "TARGET"
    And token "h" reads no policy-triggered run of stack "target" from stack "infra" run "source" through B
    And the answer's items is '[]'
    When token "h" sends "GET /api/v1/runs/{source}" through A
    Then it answers 200
    And the answer's flags is '["account-private","policy.internal_error"]'
    And token "s" reads no policy-triggered run of stack "foreign" from stack "infra" run "source" through B

  @case:failure @bitbucket-cloud
  Scenario: Deleting a modeled provider integration refuses its target despite a retained tracked commit
    Given a Bitbucket Cloud account "dev@acme.example" with an API token kept as "tok"
    And a Bitbucket Cloud repository "acme/infra" that "dev@acme.example" administers, its head of "main" kept as "cloudSeed"
    And token "h" sends "POST /api/v1/bitbucket-cloud-integrations" with '{"email":"dev@acme.example","apiToken":"{tok}","customInput":{"name":"Cloud","spaceID":"legacy"}}' through A
    And it answers 201
    And the answer's id names "integration"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Detached","namespace":"acme","repository":"infra","branch":"main","provider":"BITBUCKET_CLOUD","vcsIntegrationId":"{integration}","space":"legacy"},"manageState":false}' through B
    And it answers 201
    And token "h" eventually reads the stack "detached" tracking the commit kept as "cloudSeed" through A
    When token "h" sends "DELETE /api/v1/bitbucket-cloud-integrations/{integration}" through B
    Then it answers 200
    And token "h" eventually reads the stack "detached" tracking the commit kept as "cloudSeed" through A
    And the answer's vcsDetached is 'true'
    And token "h" sends "POST /api/v1/policies" with '{"name":"Detached source guard","type":"TRIGGER","space":"legacy","body":"package infraweave\ntrigger contains name if { some name in {\"target\", \"detached\"} }\n"}' through A
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
    And token "h" reads no policy-triggered run of stack "target" from stack "infra" run "source" through B
    And token "h" reads no policy-triggered run of stack "detached" from stack "infra" run "source" through A
