@postgres @objectstore @vcs @docker @tofu
Feature: Workflow command templates rendered by the runner
  A stack's repository replaces OpenTofu's built-in commands with
  .infraweave/workflow.yml, whose nine keys are text templates. The runner
  reads the file from the project root before initialization, checks
  every key before any command runs, and renders each phase's commands
  with the run's parameters when the phase runs, every value it embeds
  one shell word. A real launcher of a private pool runs each run in a
  container of infraweave-runner-opentofu:acceptance with the newest
  OpenTofu the version catalogue lists. The release indexes serve the
  recorded fixtures; the home key is space-admin on the account's top
  space. Stack infra lives in legacy, keeps its state in its workspace and
  runs on the pool; its configuration has a terraform_data resource
  alone, so no provider is downloaded.
  ref: decision 0024
  ref: decision 0008

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  @op:runTrigger @case:success
  Scenario: A workspace name a shell would split into a command reaches the plan command as one word
    The workspace name x&touch$IFS@pwned, unquoted, would run touch @pwned
    in the background of the echo. The plan command echoes the name and
    plans only when no @pwned exists.
    Given a Gitea repository "infra" with the files:
      | main.tf                  | resource "terraform_data" "this" {\n  input = "one"\n}\n |
      | .infraweave/workflow.yml | init: tofu init -input=false -lock-timeout=${{ lockTimeout }}\nworkspaceSelect: tofu workspace select ${{ workspaceName }}\nworkspaceNew: tofu workspace new ${{ workspaceName }}\nplan: echo plan in workspace ${{ workspaceName }} && test ! -e @pwned && tofu plan -input=false -lock=${{ lock }} -out=${{ planFileName }}\nshowState: tofu show -json\nshowPlan: tofu show -json ${{ planFileName }}\ngetOutputs: tofu output -json\napply: tofu apply -input=false ${{ planFileName }}\ndestroy: tofu destroy -auto-approve -input=false\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{"workspace":"x&touch$IFS@pwned"}}},"manageState":false}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, PLANNING, UNCONFIRMED"
    And token "h" reads the run "run"'s PLANNING log through B, which holds "plan in workspace x&touch$IFS@pwned\n" and not "plan in workspace x\n"

  @op:runTrigger @case:failure
  Scenario: A workflow file whose template does not type-check fails the run in INITIALIZING, naming the key and its range
    Given a Gitea repository "infra" with the files:
      | main.tf                  | resource "terraform_data" "this" {\n  input = "one"\n}\n |
      | .infraweave/workflow.yml | init: tofu init -input=false -lock-timeout=${{ lockTimeout }}\nworkspaceSelect: tofu workspace select ${{ workspaceName }}\nworkspaceNew: tofu workspace new ${{ workspaceName }}\nplan: tofu plan -input=false -lock=${{ lock + 1 }} -out=${{ planFileName }}\nshowState: tofu show -json\nshowPlan: tofu show -json ${{ planFileName }}\ngetOutputs: tofu output -json\napply: tofu apply -input=false ${{ planFileName }}\ndestroy: tofu destroy -auto-approve -input=false\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","vendorConfig":{"opentofu":{}}},"manageState":false}' through B
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FAILED" through A
    And the answer's history is the states "QUEUED, READY, PREPARING, INITIALIZING, FAILED"
    And the answer's last state transition's note is "The workflow file .infraweave/workflow.yml cannot be used: a template does not check: plan (line 4): 1:39: found no matching overload for '_+_' applied to '(bool, int)'."
