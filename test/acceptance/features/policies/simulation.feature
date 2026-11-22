@postgres @objectstore @policy-phase
Feature: Edited policy simulation uses the live runtime without persistence
  A simulation compiles the submitted policy with the normal preamble and
  capabilities and evaluates edited JSON. It never applies the resulting
  decision to sessions, runs, samples, captures or budgets.
  ref: decision 0009; #663

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"

  @op:policySimulatev2 @case:success
  Scenario: A policy author simulates edited PLAN input through the real endpoint
    When token "h" sends "POST /api/v1/policies/simulate" with '{"type":"PLAN","engineType":"REGO_V1","body":"package infraweave\nwarn contains \"edited-input\" if { input.value }\n","input":"{\"value\":true}"}' through B
    Then it answers 200
