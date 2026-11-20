@terraform-plans @postgres @objectstore @vcs @docker @terraform @tofu @policy-plan-worker
Feature: Native worker reports preserve policy flag owners across repeated targeted plans
  Real worker uploads drive two independently stored Rego programs. A targeted
  plan resets one owner; another resets and adds the shared flag before its peer
  removes its own copy. Terminal triggering and a new run use separate ownership
  while previous_run retains the committed source snapshot.
  ref: decision 0009
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And token "h" eventually reads the OpenTofu versions through B as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  @op:runTargetedReplan @case:success
  Scenario Outline: Actual <vendor> reports preserve owners, reset order and previous-run snapshots
    Given a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "alpha" {\n  input = "alpha-654"\n}\nresource "terraform_data" "beta" {\n  input = "beta-654"\n}\n |
    And a Gitea repository "downstream" with the files:
      | main.tf | resource "terraform_data" "isolated" {\n  input = "target-654"\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And the head of "main" of "downstream" is kept as "targetSeed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"<vendor>":{}}},"manageState":true}' through A
    And it answers 201
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Target","repository":"downstream","branch":"main","provider":"GIT","repositoryURL":"{downstream.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"vendorConfig":{"<vendor>":{}}},"manageState":true}' through B
    And it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    And token "h" eventually reads the stack "target" tracking the commit kept as "targetSeed" through B
    And token "h" sends "POST /api/v1/policies" with '{"name":"Alpha","type":"PLAN","space":"legacy","engineType":"REGO_V1","body":"package infraweave\naddresses := {change.address | some change in input.<vendor>.resource_changes}\nnative if { count(input.<vendor>.<version>) > 0; input.<vendor>.resource_changes == input.terraform.resource_changes }\nfull if { native; count(addresses) == 2 }\nalpha_only if { native; addresses == {\"terraform_data.alpha\"} }\nbeta_only if { native; addresses == {\"terraform_data.beta\"} }\nwarn contains \"Review native ownership\" if true\nflag contains \"a-old\" if full\nflag contains \"shared\" if full\nflag contains \"a-new\" if alpha_only\nflag contains \"saw-initial\" if { alpha_only; \"a-old\" in input.infraweave.run.flags }\nreset_flag contains \"a-old\" if alpha_only\nreset_flag contains \"shared\" if alpha_only\nflag contains \"a-last\" if beta_only\nflag contains \"shared\" if beta_only\nflag contains \"saw-later\" if { beta_only; \"a-new\" in input.infraweave.run.flags }\nreset_flag contains \"a-new\" if beta_only\nreset_flag contains \"shared\" if beta_only\nreset_flag contains \"saw-initial\" if beta_only\nflag contains \"previous-owners\" if { prev := input.infraweave.previous_run; every expected in {\"a-last\", \"b-last\", \"shared\", \"trigger-source\"} { expected in prev.flags } }\n"}' through A
    And it answers 201
    And the answer's id names "Alpha"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{Alpha}" through B
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '{"name":"Beta","type":"PLAN","space":"legacy","engineType":"REGO_V0","body":"package infraweave\naddresses := {change.address | change := input.terraform.resource_changes[_]}\nbeta_only { addresses == {\"terraform_data.beta\"} }\nflag[\"shared\"] { not beta_only }\nflag[\"b-old\"] { not beta_only }\nflag[\"b-last\"] { beta_only }\nreset_flag[\"shared\"] { beta_only }\nreset_flag[\"b-old\"] { beta_only }\n"}' through A
    And it answers 201
    And the answer's id names "Beta"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{Beta}" through B
    And it answers 200
    And token "h" sends "POST /api/v1/policies" with '{"name":"Terminal","type":"TRIGGER","space":"legacy","engineType":"REGO_V1","body":"package infraweave\ntrigger contains \"target\" if { input.run.state == \"DISCARDED\"; every expected in {\"a-last\", \"b-last\", \"shared\"} { expected in input.run.flags } }\nflag contains \"trigger-source\" if count(trigger) > 0\n"}' through A
    And it answers 201
    And the answer's id names "Terminal"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{Terminal}" through B
    And it answers 200
    And the workflow fixture pauses the native PLAN projection
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"TRACKED","commitSha":"{seed}"}' through A
    Then it answers 200
    And the answer's id names "run"
    And the actual PLAN report for run "run" waits without committed policy effects
    When token "h" sends "GET /api/v1/runs/{run}" through B
    Then it answers 200
    And the answer's state is '"PLANNING"'
    And the answer's flags is '[]'
    And the answer's PLAN receipt outcomes are ""
    And the committed workflow update of run "run" matches the public policy snapshot
    And the workflow fixture aborts that report transaction and releases the real worker retry
    And token "h" eventually reads the run "run" in the state "UNCONFIRMED" through B
    And the answer's flags is '["a-old","b-old","shared"]'
    And the answer's PLAN receipt outcomes are "ALLOW, ALLOW"
    And the committed workflow update of run "run" matches the public policy snapshot
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/targeted-replan" with '{"targets":["terraform_data.alpha"]}' through A
    Then it answers 200
    And token "h" eventually reads run "run" flags '["a-new","b-old","saw-initial","shared"]' through B
    And the answer's state is '"UNCONFIRMED"'
    And the answer's PLAN receipt outcomes are "ALLOW, ALLOW, ALLOW, ALLOW"
    And the committed workflow update of run "run" matches the public policy snapshot
    When token "h" sends "POST /api/v1/stacks/infra/runs/{run}/targeted-replan" with '{"targets":["terraform_data.beta"]}' through B
    Then it answers 200
    And token "h" eventually reads run "run" flags '["a-last","b-last","saw-later","shared"]' through A
    And the answer's state is '"UNCONFIRMED"'
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED, REPLAN_REQUESTED, PREPARING_REPLAN, PLANNING, UNCONFIRMED, REPLAN_REQUESTED, PREPARING_REPLAN, PLANNING, UNCONFIRMED"
    And the answer's PLAN receipt outcomes are "ALLOW, ALLOW, ALLOW, ALLOW, ALLOW, ALLOW"
    And the answer's plan policy outcomes are '[{"deny":[],"warn":["Review native ownership"]},{"deny":[],"warn":["Review native ownership"]},{"deny":[],"warn":["Review native ownership"]}]'
    And the committed workflow update of run "run" matches the public policy snapshot
    And the run "run" recorded 3 worker plan artifacts
    When token "h" sends "POST /api/v1/stacks/infra/runs/discard" with '{"run":"{run}"}' through B
    Then it answers 200
    And token "h" eventually reads the single policy-triggered run of stack "target" from stack "infra" run "run" as "child" through A
    And token "h" eventually reads the run "child" in the state "FINISHED" through B
    And the answer's commit hash is "{targetSeed}"
    And the answer's flags is '[]'
    And the committed workflow update of run "child" matches the public policy snapshot
    When token "h" sends "GET /api/v1/runs/{run}" through A
    Then it answers 200
    And the answer's flags is '["a-last","b-last","saw-later","shared","trigger-source"]'
    And the answer's TRIGGER receipt outcomes are "TRIGGER"
    And the committed workflow update of run "run" matches the public policy snapshot
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"runType":"PROPOSED","commitSha":"{seed}"}' through A
    Then it answers 200
    And the answer's id names "next"
    And token "h" eventually reads the run "next" in the state "FINISHED" through B
    And the answer's flags is '["a-old","b-old","previous-owners","shared"]'
    And the answer's PLAN receipt outcomes are "ALLOW, ALLOW"
    And the committed workflow update of run "next" matches the public policy snapshot

    Examples:
      | vendor    | version           |
      | terraform | terraform_version |
      | opentofu  | opentofu_version  |
