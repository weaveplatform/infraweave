@postgres @objectstore @policies
Feature: Simulation enforces public compiler and input boundaries
  Policy source and decoded input have domain ceilings as well as the shared
  HTTP envelope. Limits and failures never become retained evaluation evidence.
  ref: decision 0009; #663

  @op:policySimulatev2 @case:success @case:failure
  Scenario Outline: The <field> boundary is enforced after JSON-string decoding
    Given simulation boundary tests use a 32 MiB HTTP envelope
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the simulation's evaluation and workflow state is recorded
    When token "h" simulates a <field> of <accepted> bytes through A
    Then it answers 200
    When token "h" simulates a <field> of <refused> bytes through B
    Then it answers a 422 problem "validation-failed"
    And the simulation leaves evaluation, workflow and object storage unchanged

    Examples:
      | field | accepted | refused |
      | body  | 262144   | 262145  |
      | input | 4194304  | 4194305 |

  @op:policySimulatev2 @case:failure
  Scenario: The normal HTTP envelope remains stricter when configured below the simulation ceiling
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the simulation's evaluation and workflow state is recorded
    When token "h" simulates a input of 1100000 bytes through B
    Then it answers a 413 problem "payload-too-large"
    And the simulation leaves evaluation, workflow and object storage unchanged

  @op:policySimulatev2 @case:failure
  Scenario: The configured PLAN deadline bounds real public OPA evaluation
    Given the PLAN simulation deadline is 200 milliseconds
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the simulation's evaluation and workflow state is recorded
    When an expensive PLAN simulation is attempted through B
    Then it answers a 422 problem "validation-failed"
    And the simulation leaves evaluation, workflow and object storage unchanged

  @op:policySimulatev2 @case:denial
  Scenario: Switching off policies refuses simulation before execution
    Given the installation switches off the features "policies"
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    When token "h" sends "POST /api/v1/policies/simulate" with '{"type":"PLAN","body":"package infraweave","input":"{}"}' through B
    Then it answers a 403 problem "feature-disabled"
