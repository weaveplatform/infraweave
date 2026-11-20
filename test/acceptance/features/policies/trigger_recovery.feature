@postgres @objectstore @vcs @docker @terraform @policy-trigger-worker
Feature: Terminal trigger fan-out survives actual replica crashes
  Real queue claims and completions pause at database boundaries. Both server
  processes die; committed source state survives, provisional targets and
  receipts do not. Replacement replicas recover the job and real workers
  finish each target exactly once on its own commit.
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

    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Buddy","repository":"downstream","branch":"main","provider":"GIT","repositoryURL":"{downstream.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"terraform":{}}},"manageState":true}' through A
    And it answers 201
    And token "h" eventually reads the stack "buddy" tracking the commit kept as "targetSeed" through B
    And token "h" sends "POST /api/v1/policies" with '{"name":"Atomic fan-out","type":"TRIGGER","space":"legacy","body":"package infraweave\ntrigger contains name if { input.run.state == \"CANCELED\"; some name in {\"target\", \"buddy\"} }\nflag contains \"source-recovered\" if { count(trigger) == 2 }\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200

  @case:success
  Scenario Outline: A crash at queue <boundary> recovers one complete fan-out
    Given the trigger fixture pauses queue <boundary>
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{sourceSeed}","runType":"TRACKED"}' through A
    Then it answers 200
    And the answer's id names "source"
    When token "h" sends "POST /api/v1/stacks/infra/runs/{source}/cancel" with '{}' through B
    Then it answers 200
    And both trigger replicas die at the queue boundary for source "source" without partial fan-out
    And replacement trigger replicas recover the durable job for source "source"
    And token "h" eventually reads the single policy-triggered run of stack "target" from stack "infra" run "source" as "child" through A
    And token "h" eventually reads the single policy-triggered run of stack "buddy" from stack "infra" run "source" as "buddyRun" through B
    And the durable trigger job for source "source" has completed once
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B
    And token "h" eventually reads the run "child" in the state "FINISHED" through A
    And the answer's commit hash is "{targetSeed}"
    And the answer's triggeredBy is '"policy/infra/{source}"'
    And the answer's flags is '[]'
    And token "h" eventually reads the run "buddyRun" in the state "FINISHED" through B
    And the answer's commit hash is "{targetSeed}"
    And the answer's flags is '[]'
    When token "h" sends "GET /api/v1/runs/{source}" through A
    Then it answers 200
    And the answer's flags is '["source-recovered"]'
    And the answer's TRIGGER receipt outcomes are "TRIGGER"
    And token "h" eventually reads the single policy-triggered run of stack "target" from stack "infra" run "source" as "child" through B
    And token "h" eventually reads the single policy-triggered run of stack "buddy" from stack "infra" run "source" as "buddyRun" through A

    Examples:
      | boundary   |
      | claim      |
      | completion |
