@postgres @policy-management
Feature: Search persisted policies within the caller's readable spaces

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"

  @op:policies @op:policy @op:policyUpdatev2 @op:policyUpdatev2Patch @op:policyDelete @op:searchPolicies @op:searchPoliciesSuggestions @case:failure
  Scenario: A sibling account cannot read or change a known policy ID
    Given the sibling key is exchanged through B for token "s"
    And token "h" sends "POST /api/v1/policies" with '{"name":"Private guard","type":"PLAN","labels":["private"],"body":"package infraweave\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    When token "s" sends "GET /api/v1/policies/{policy}" through B
    Then it answers a 404 problem "not-found"
    When token "s" sends "PUT /api/v1/policies/{policy}" with '{"name":"Stolen","body":"package infraweave\n"}' through A
    Then it answers a 404 problem "not-found"
    When token "s" sends "PATCH /api/v1/policies/{policy}" with '{"name":"Stolen"}' through B
    Then it answers a 404 problem "not-found"
    When token "s" sends "DELETE /api/v1/policies/{policy}?force=true" through A
    Then it answers a 404 problem "not-found"
    When token "s" sends "GET /api/v1/policies" through B
    Then the policy page IDs are ""
    When token "s" sends "POST /api/v1/policies/search" with '{}' through A
    Then the policy page IDs are ""
    When token "s" sends "POST /api/v1/policies/search/suggestions" with '{"fields":["label"]}' through B
    Then it answers 200
    And the answer's filteredCount is '0'
    And the policy search field "label" is '{"name":"label","filterable":true,"type":"STRING","orderable":false,"possibleValuesString":{"values":[],"counts":[]}}'
    When token "h" sends "GET /api/v1/policies/{policy}" through A
    Then it answers 200
    And the answer's name is '"Private guard"'

  @op:policies @op:searchPolicies @case:success
  Scenario: Both list and search cursors resume on another replica without skipped policies
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Alpha","type":"PLAN","body":"package infraweave\n"}' through A
    And it answers 201
    And the answer's id names "first"
    And token "h" sends "POST /api/v1/policies" with '{"name":"Beta","type":"PLAN","body":"package infraweave\n"}' through B
    And it answers 201
    And the answer's id names "second"
    When token "h" sends "GET /api/v1/policies?limit=1" through A
    Then the policy page IDs are "{first}"
    And the policy list cursor is kept as "list-next"
    When token "h" sends "GET /api/v1/policies?limit=1&cursor={list-next}" through B
    Then the policy page IDs are "{second}"
    And the answer leaves out "nextCursor"
    When token "h" sends "POST /api/v1/policies/search" with '{"first":1,"orderBy":[{"field":"name","direction":"DESC"}]}' through B
    Then the policy page IDs are "{second}"
    And the policy search cursor is kept as "search-next"
    When token "h" sends "POST /api/v1/policies/search" with '{"first":1,"orderBy":[{"field":"name","direction":"DESC"}],"after":"{search-next}"}' through A
    Then the policy page IDs are "{first}"

  @op:policies @op:searchPolicies @op:searchPoliciesSuggestions @op:searchSpacePolicies @op:searchSpacePoliciesSuggestions @case:failure
  Scenario: Invalid policy search requests and unknown spaces have public failures
    When token "h" sends "GET /api/v1/policies?cursor=nope" through A
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "POST /api/v1/policies/search" with '{"after":"nope"}' through B
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "POST /api/v1/policies/search/suggestions" with '{"predicates":[{"field":"missing","constraint":{"stringEquals":["no"]}}]}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "POST /api/v1/space-policies/search" with '{"space":"nowhere","input":{}}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/space-policies/search/suggestions" with '{"space":"nowhere","input":{}}' through A
    Then it answers a 404 problem "not-found"

  @op:policies @op:searchPolicies @op:searchPoliciesSuggestions @op:searchSpacePolicies @op:searchSpacePoliciesSuggestions @case:success @case:denial
  Scenario: Searches and facet counts exclude policies in unreadable spaces
    Given the member key is exchanged through B for token "m"
    And the member key is space-reader on the space "legacy"
    And token "h" sends "POST /api/v1/policies" with '{"name":"A readable","type":"PLAN","space":"legacy","labels":["public"],"body":"package infraweave\n"}' through A
    And it answers 201
    And the answer's id names "readable"
    And token "h" sends "POST /api/v1/policies" with '{"name":"B hidden","type":"PLAN","labels":["private"],"body":"package infraweave\n"}' through B
    And it answers 201
    When token "m" sends "GET /api/v1/policies?limit=1" through B
    Then the policy page IDs are "{readable}"
    And the answer leaves out "nextCursor"
    When token "m" sends "POST /api/v1/policies/search" with '{"first":1}' through A
    Then the policy page IDs are "{readable}"
    When token "m" sends "POST /api/v1/policies/search/suggestions" with '{"fields":["label"]}' through B
    Then it answers 200
    And the answer's filteredCount is '1'
    And the policy search field "label" is '{"name":"label","filterable":true,"type":"STRING","orderable":false,"possibleValuesString":{"values":["public"],"counts":[1]}}'
    When token "m" sends "POST /api/v1/space-policies/search" with '{"space":"legacy","input":{}}' through A
    Then the policy page IDs are "{readable}"
    When token "m" sends "POST /api/v1/space-policies/search/suggestions" with '{"space":"legacy","input":{"fields":["label"]}}' through B
    Then it answers 200
    And the answer's filteredCount is '1'
    When token "m" sends "POST /api/v1/space-policies/search" with '{"space":"acme","input":{}}' through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/space-policies/search/suggestions" with '{"space":"acme","input":{}}' through B
    Then it answers a 404 problem "not-found"

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
