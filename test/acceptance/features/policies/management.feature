@postgres @policy-management
Feature: Persisted policy management
  Policies belong to one account and space. A save compiles the stored body
  with the selected per-policy dialect before committing. Management does not
  imply delivery of the policy type's workflow integration.

  @op:policyCreatev2 @op:policy @case:success
  Scenario: A compiled policy persists across replicas with the intentional V1 default
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    When token "h" sends "POST /api/v1/policies" with '{"name":"Plan guard","type":"PLAN","space":"acme","labels":["prod","eu"],"body":"package infraweave\nwarn contains \"review\" if { true }\n"}' through A
    Then it answers 201
    And the answer's id names "policy"
    And the answer's engineType is '"REGO_V1"'
    And the answer's type is '"PLAN"'
    When token "h" sends "GET /api/v1/policies/{policy}" through B
    Then it answers 200
    And the answer's id is '"{policy}"'
    And the answer's name is '"Plan guard"'
    And the answer's engineType is '"REGO_V1"'
    And the answer's body is '"package infraweave\nwarn contains \"review\" if { true }\n"'
