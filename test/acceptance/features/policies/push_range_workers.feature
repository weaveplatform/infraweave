@policy-push-range @postgres @objectstore @github @gitlab @docker
Feature: Reverted pushed paths still run actual workers through default selection
  A final tree equal to the original tree still includes the intermediate
  touched file, and a repeated delivery creates no second run.
  ref: decision 0008
  ref: decision 0012
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And token "h" eventually reads the OpenTofu versions through B as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"acme","certificateSigningRequest":"{csr}"}' through A
    And it answers 201
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  Scenario Outline: Default <provider> selection plans an edited and reverted path with <vendor>
    Given a "<provider>" plan-comment stack "infra" using "<vendor>" with labels '[]'
    And the event repository prepares a real native edit and revert on "main" as "collected"
    And the event push uses "missing" native commit metadata
    When the event fixture pushes "range" from "seed" to "collected" on "main" with "valid" verification through B and receives 202
    Then event delivery "range" settles with 1 new runs
    And event delivery "range" retains a recovered "main" range of 2 commits and touched paths '["main.tf"]' with native omission "absent"
    And the "TRACKED" event run on stack "infra" for commit "collected" is kept as "run"
    And token "h" eventually reads the run "run" in the state "UNCONFIRMED" through A
    And the event run "run" has a real tool plan through A
    When the event fixture pushes "redelivery" from "seed" to "collected" on "main" with "valid" verification through A and receives 202
    Then event delivery "redelivery" settles with 0 new runs
    And stack "infra" has exactly 1 event runs for commit "collected"

    @terraform
    Examples:
      | provider | vendor    |
      | GitHub   | terraform |

    @tofu
    Examples:
      | provider | vendor   |
      | GitLab   | opentofu |
