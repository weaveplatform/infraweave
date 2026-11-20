@policy-push-preparation @postgres @objectstore @github
Feature: Push selection remains authoritative after preparation
  A policy attached after the no-policy preparation hint cannot cause an
  unvalidated default run or lose its durable failed-policy feedback.
  ref: decision 0009
  ref: decision 0012
  ref: decision 0016

  Scenario: A late policy on a headless GitHub comment fails with durable feedback
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a "GitHub" plan-comment stack "infra" using "terraform" with labels '[]'
    And the event fixture sends request "request" through A
    And event delivery "request" settles with 0 new runs
    And the event fixture holds future child evaluations
    When the event fixture sends comment "late" through B
    Then event delivery "late" has a held child without native request facts
    When token "h" sends "POST /api/v1/policies" with '{"name":"Late invalid priority","type":"GIT_PUSH","body":"package infraweave\npropose := true\npriority := \"invalid\"\n"}' through A
    Then it answers 201
    And the answer's id names "policy"
    When token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    Then it answers 200
    When the event fixture releases child evaluations
    Then event delivery "late" commits a durable failed policy check
    And event delivery "late" settles with 0 new runs
