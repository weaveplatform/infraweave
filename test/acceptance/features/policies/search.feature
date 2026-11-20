@postgres @policy-management
Feature: Search persisted policies within the caller's readable spaces

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"

  @op:policies @op:searchPolicies @op:searchPoliciesSuggestions @op:searchSpacePolicies @op:searchSpacePoliciesSuggestions @case:success
  Scenario: Account and inherited space searches use persistent policy projections
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And it answers 201
    And the answer's id is the space "team"
    And token "h" sends "POST /api/v1/policies" with '{"name":"A root guard","type":"PLAN","labels":["eu"],"body":"package infraweave\n"}' through A
    And it answers 201
    And the answer's id names "root"
    And token "h" sends "POST /api/v1/policies" with '{"name":"B team guard","type":"APPROVAL","space":"{team}","labels":["eu","prod"],"body":"package infraweave\n"}' through B
    And it answers 201
    And the answer's id names "own"
    And token "h" sends "POST /api/v1/policies" with '{"name":"C legacy guard","type":"TASK","space":"legacy","labels":["elsewhere"],"body":"package infraweave\n"}' through A
    And it answers 201
    And the answer's id names "other"
    When token "h" sends "GET /api/v1/policies" through B
    Then the policy page IDs are "{root}, {own}, {other}"
    When token "h" sends "POST /api/v1/policies/search" with '{"orderBy":[{"field":"name","direction":"ASC"}]}' through B
    Then the policy page IDs are "{root}, {own}, {other}"
    When token "h" sends "POST /api/v1/policies/search/suggestions" with '{"fields":["label"]}' through A
    Then it answers 200
    And the answer's filteredCount is '3'
    And the policy search field "label" is '{"name":"label","filterable":true,"type":"STRING","orderable":false,"possibleValuesString":{"values":["eu","elsewhere","prod"],"counts":[2,1,1]}}'
    When token "h" sends "POST /api/v1/space-policies/search" with '{"space":"{team}","input":{"orderBy":[{"field":"name","direction":"ASC"}]}}' through B
    Then the policy page IDs are "{root}, {own}"
    And the policy inheritance flags are "true, false"
    When token "h" sends "POST /api/v1/space-policies/search/suggestions" with '{"space":"{team}","input":{"fields":["label"]}}' through A
    Then it answers 200
    And the answer's filteredCount is '2'
    And the policy search field "label" is '{"name":"label","filterable":true,"type":"STRING","orderable":false,"possibleValuesString":{"values":["eu","prod"],"counts":[2,1]}}'
