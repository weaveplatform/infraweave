@postgres @objectstore @vcs @docker @tofu
Feature: A run's secrets reach its log masked, and its plan reaches the store sealed
  The runner masks, as five asterisks, every write-only variable's value
  the run's log would hold, every word a hook names with ::add-mask from
  then on, and, on a stack that enables well-known secret masking, every
  value of a well-known pattern, such as an AWS access key ID. The plan's
  JSON, its changes, the saved workspace and the state reach the store
  only sealed, so no object holds a value of the plan in the clear, even
  one the configuration marks sensitive. A real launcher of a private pool
  runs the run with the newest OpenTofu the version catalogue lists. The
  release indexes serve the recorded fixtures; the home key is
  space-admin on the account's top space. Stack infra lives in legacy,
  keeps its state in infraweave and runs on the pool; the repository's
  configuration has a terraform_data resource, so no provider is
  downloaded.
  ref: decision 0013
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the OpenTofu versions through A as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And a Gitea repository "infra" with the files:
      | main.tf | resource "terraform_data" "this" {\n  input = sensitive("pl4n-0nly-v4lue")\n}\n |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"legacy","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  @op:runTrigger @op:runLogs @case:success
  Scenario: A tracked run's hooks print a secret variable, an ::add-mask word and a well-known token, and its log holds five asterisks for each
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GIT","repositoryURL":"{infra.url}","space":"legacy","workerPool":"{pool}","autodeploy":true,"enableWellKnownSecretMasking":true,"vendorConfig":{"opentofu":{}},"afterPlan":["echo \"secret: $SECRET\"","echo ::add-mask m4sk-w0rd","echo \"word: m4sk-w0rd\"","echo \"key: AKIAIOSFODNN7EXAMPLE\""]},"manageState":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"SECRET","type":"ENVIRONMENT_VARIABLE","value":"d3l1very-s3cr3t","writeOnly":true}' through A
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    Then token "h" eventually reads the run "run" in the state "FINISHED" through A
    And token "h" reads the run "run"'s PLANNING log through B, which holds "secret: *****\n::add-mask *****\nword: *****\nkey: *****" and not "m4sk-w0rd"
    And token "h" eventually reads a download URL of the run "run"'s PLANNING log through A
    And the answer's url downloads a log that holds "secret: *****\n::add-mask *****\nword: *****\nkey: *****" and not "AKIAIOSFODNN7EXAMPLE"
    And the run "run" wrote 1 state version
    And no object in the scenario's bucket holds "pl4n-0nly-v4lue", of at least 6 objects
    And no object in the scenario's bucket holds "d3l1very-s3cr3t", of at least 6 objects
    And no object in the scenario's bucket holds "m4sk-w0rd", of at least 6 objects
