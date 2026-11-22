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
    Given the simulation's evaluation and workflow state is recorded
    When token "h" sends "POST /api/v1/policies/simulate" with '{"type":"PLAN","engineType":"REGO_V1","body":"package infraweave\nwarn contains \"edited-input\" if { input.value }\n","input":"{\"value\":true}"}' through B
    Then it answers 200
    And the simulation decision is '{"deny":[],"flag":[],"info":[],"reset_flag":[],"sample":false,"warn":["edited-input"]}'
    And the simulation leaves evaluation, workflow and object storage unchanged

  @op:policySimulatev2 @case:success
  Scenario: Explicit V0 and omitted-engine V1 simulate edited input without retaining a true sample
    Given the simulation's evaluation and workflow state is recorded
    When token "h" sends "POST /api/v1/policies/simulate" with '{"type":"PLAN","engineType":"REGO_V0","body":"package infraweave\nsample := true\nwarn[\"legacy\"] { input.value }\n","input":"{\"value\":true}"}' through A
    Then it answers 200
    And the simulation decision is '{"deny":[],"flag":[],"info":[],"reset_flag":[],"sample":true,"warn":["legacy"]}'
    When token "h" sends "POST /api/v1/policies/simulate" with '{"type":"PLAN","body":"package infraweave\nsample := true\nwarn contains \"default-v1\" if input.value\n","input":"{\"value\":true}"}' through B
    Then it answers 200
    And the simulation decision is '{"deny":[],"flag":[],"info":[],"reset_flag":[],"sample":true,"warn":["default-v1"]}'
    And the simulation leaves evaluation, workflow and object storage unchanged

  @op:policySimulatev2 @case:success @case:failure
  Scenario Outline: Recorded <recording> decisions are replayed through the public simulator
    Given the simulation's evaluation and workflow state is recorded
    When recorded policy simulations "<recording>" agree through the public endpoint
    Then the simulation leaves evaluation, workflow and object storage unchanged

    Examples:
      | recording |
      | preambles |
      | roles     |
      | trigger   |
      | C273      |
      | C192      |

  @op:policySimulatev2 @case:denial
  Scenario: A caller with no readable space cannot simulate account policy code
    Given the member key is exchanged through A for token "m"
    And the simulation's evaluation and workflow state is recorded
    When token "m" sends "POST /api/v1/policies/simulate" with '{"type":"PLAN","body":"package infraweave","input":"{}"}' through B
    Then it answers a 403 problem "forbidden"
    And the simulation leaves evaluation, workflow and object storage unchanged

  @op:policySimulatev2 @case:failure
  Scenario Outline: Invalid simulation <kind> never records evaluation evidence
    Given the simulation's evaluation and workflow state is recorded
    When token "h" sends "POST /api/v1/policies/simulate" with '<request>' through A
    Then it answers a 422 problem "validation-failed"
    And the simulation leaves evaluation, workflow and object storage unchanged

    Examples:
      | kind | request |
      | JSON | {"type":"PLAN","body":"package infraweave","input":"{private-input"} |
      | source | {"type":"PLAN","body":"package infraweave\\nwarn contains","input":"{}"} |
      | dialect | {"type":"PLAN","engineType":"REGO_V1","body":"package infraweave\\nwarn[\"legacy\"] { true }","input":"{}"} |
      | package | {"type":"PLAN","body":"package other","input":"{}"} |
      | exec | {"type":"PLAN","body":"package infraweave\\nwarn contains exec(\"id\")","input":"{}"} |
      | HTTP | {"type":"PLAN","body":"package infraweave\\nwarn contains http.send({\"url\":\"https://invalid.test\"})","input":"{}"} |
      | libraries | {"type":"PLAN","body":"package infraweave","input":"{}","libraries":{"libraries":[{"name":"caller","body":"package library"}]}} |

  @op:policySimulatev2 @case:failure
  Scenario: Unauthenticated callers and unknown dialects cannot simulate
    When the browser sends "POST /api/v1/policies/simulate" with '{"type":"PLAN","body":"package infraweave","input":"{}"}' through A
    Then it answers a 401 problem "unauthenticated"
    When token "h" sends "POST /api/v1/policies/simulate" with '{"type":"PLAN","engineType":"REGO_V2","body":"package infraweave","input":"{}"}' through B
    Then it answers a 400 problem "invalid-request"
