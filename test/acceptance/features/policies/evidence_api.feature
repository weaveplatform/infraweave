@postgres @policy-phase @policy-evidence
Feature: Policy sample and capture APIs enforce evidence ownership
  Public readers must distinguish safe metadata from private input and print.
  Root administration is required for captured input, even when a caller
  administers the owning child space. Account ownership precedes object reads.
  ref: decisions 0003, 0007 and 0009; #662

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"

  @op:policyInputCaptures @case:success
  Scenario: A root administrator can list an empty capture collection in an owned space
    When token "h" sends "GET /api/v1/policy-input-captures?space=legacy&type=PLAN" through B
    Then it answers 200
    And the answer's items is '[]'
