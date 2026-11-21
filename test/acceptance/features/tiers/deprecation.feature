@postgres @policy-task
Feature: Deprecated policy types carry an explicit migration notice
  TASK remains available as a deprecated policy, while its superseded alias
  stays outside the served enum. The date belongs to infraweave's contract.
  ref: decisions 0009 and 0021; #659

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"

  @op:policyCreatev2 @op:policy @op:policyUpdatev2 @op:policyUpdatev2Patch @op:policyDelete @case:success @case:failure
  Scenario: Known TASK policy responses retain the notice through reads, edits, compiler refusal and deletion
    When token "h" sends "POST /api/v1/policies" with '{"name":"Deprecated command guard","type":"TASK","body":"package infraweave\n"}' through A
    Then it answers 201
    And the TASK answer carries its deprecation notice
    And the answer's id names "guard"
    When token "h" sends "GET /api/v1/policies/{guard}" through B
    Then it answers 200
    And the TASK answer carries its deprecation notice
    When token "h" sends "PUT /api/v1/policies/{guard}" with '{"name":"Edited command guard","body":"package infraweave\n"}' through A
    Then it answers 200
    And the TASK answer carries its deprecation notice
    When token "h" sends "PATCH /api/v1/policies/{guard}" with '{"body":"package infraweave\ndeny if {\n"}' through B
    Then it answers a 422 problem "validation-failed"
    And the TASK answer carries its deprecation notice
    When token "h" sends "DELETE /api/v1/policies/{guard}" through B
    Then it answers 200
    And the TASK answer carries its deprecation notice

  @op:policyCreatev2 @case:failure
  Scenario: The superseded TASK_RUN alias is refused through the actual API
    When token "h" sends "POST /api/v1/policies" with '{"name":"Unsupported alias","type":"TASK_RUN","body":"package infraweave\n"}' through A
    Then it answers a 400 problem "invalid-request"
