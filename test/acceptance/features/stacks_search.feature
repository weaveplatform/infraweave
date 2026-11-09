@postgres
Feature: Stack search and suggestions
  A search keeps the stacks of the spaces the caller may read that its
  predicates keep, in the order it names, a page at a time, read forwards
  from the first page or backwards from the last; stacks equal in the order
  follow their ids. Suggestions list the values those stacks hold for each
  field asked for, with how many hold each, and count the stacks. No stack,
  value or count comes from a space the caller cannot read. The home key is
  space-admin on the account's top space; the member key holds only the
  bindings a scenario gives it. Team sits below acme and child below team;
  infra is locked. The expensive budget is raised so a scenario reads every
  page it names.

  Background:
    Given two infraweave servers, A and B, on one database whose expensive budget is 200 at 100 a second
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Child","description":"","inheritEntities":false,"parentSpace":"{team}"}' through B
    And the answer's id is the space "child"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"zeta","branch":"main","space":"legacy","labels":["prod","eu"]},"manageState":false}' through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Web","repository":"alpha","branch":"dev","space":"{child}","labels":["prod"]},"manageState":false}' through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Api","repository":"mid","branch":"main","space":"{team}","labels":["staging"]},"manageState":false}' through A
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Core","repository":"beta","branch":"release","space":"acme"},"manageState":false}' through B
    And token "h" sends "POST /api/v1/stacks/infra/lock" through A

  @op:searchStacks @case:success
  Scenario: Predicates on labels, space, lock and name keep the stacks that meet them
    Then token "h" searching the stacks where '[{"field":"label","constraint":{"stringEquals":["prod"]}}]' through A in pages of 1 finds "infra, web" forwards and back
    And token "h" searching the stacks where '[{"field":"label","exclude":true,"constraint":{"stringEquals":["prod"]}}]' through B in pages of 1 finds "api, core" forwards and back
    And token "h" searching the stacks where '[{"field":"space","constraint":{"hierarchyNode":{"nodes":[{"equals":"{team}","includeDescendants":true}]}}}]' through A in pages of 1 finds "api, web" forwards and back
    And token "h" searching the stacks where '[{"field":"space","constraint":{"hierarchyNode":{"nodes":[{"equals":"{team}","includeDescendants":false}]}}}]' through B in pages of 1 finds "api" forwards and back
    And token "h" searching the stacks where '[{"field":"locked","constraint":{"booleanEquals":[true]}}]' through A in pages of 1 finds "infra" forwards and back
    And token "h" searching the stacks where '[{"field":"name","constraint":{"stringMatches":["*EB"]}}]' through B in pages of 1 finds "web" forwards and back
    And token "h" searching the stacks where '[{"field":"label","constraint":{"stringEquals":["prod"]}},{"field":"locked","constraint":{"booleanEquals":[false]}}]' through A in pages of 1 finds "web" forwards and back

  @op:searchStacks @case:success
  Scenario: Every order reads in full pages forwards and back
    Then token "h" searching the stacks through A in pages of 3 finds "api, core, infra, web" forwards and back
    And token "h" searching the stacks by id descending through B in pages of 3 finds "web, infra, core, api" forwards and back
    And token "h" searching the stacks by name through A in pages of 3 finds "api, core, infra, web" forwards and back
    And token "h" searching the stacks by name descending through B in pages of 3 finds "web, infra, core, api" forwards and back
    And token "h" searching the stacks by repository through A in pages of 3 finds "web, core, api, infra" forwards and back
    And token "h" searching the stacks by branch through B in pages of 3 finds "web, api, infra, core" forwards and back
    And token "h" searching the stacks by branch descending through A in pages of 3 finds "core, api, infra, web" forwards and back
    And token "h" searching the stacks by createdAt through B in pages of 3 finds "infra, web, api, core" forwards and back
    And token "h" searching the stacks by createdAt descending through A in pages of 3 finds "core, api, web, infra" forwards and back

  @op:searchStacksSuggestions @case:success
  Scenario: Suggestions count the values of the stacks the caller reads, under its predicates
    When token "h" sends "POST /api/v1/stacks/search/suggestions" with '{"fields":["label","locked","vendor"]}' through A
    Then the answer suggests the label values "prod:2, eu:1, staging:1" of 4 stacks
    And the answer suggests the locked values "false:3, true:1" of 4 stacks
    And the answer suggests the vendor values "terraform:4" of 4 stacks
    When token "h" sends "POST /api/v1/stacks/search/suggestions" with '{"fields":["label"],"predicates":[{"field":"locked","constraint":{"booleanEquals":[false]}}]}' through B
    Then the answer suggests the label values "prod:1, staging:1" of 3 stacks
    Given the member key is space-reader on the space "{team}"
    When token "m" sends "POST /api/v1/stacks/search/suggestions" with '{"fields":["label","space"]}' through A
    Then the answer suggests the label values "prod:1, staging:1" of 2 stacks
    And the answer suggests the space values "{child}:1, {team}:2" of 2 stacks

  @op:searchStacks @op:searchStacksSuggestions @case:denial
  Scenario: A member who reads one space never sees a stack, value or count from another
    Then token "m" searching the stacks through A in pages of 1 finds "" forwards and back
    When token "m" sends "POST /api/v1/stacks/search/suggestions" with '{"fields":["label"]}' through B
    Then the answer suggests the label values "" of 0 stacks
    Given the member key is space-reader on the space "legacy"
    Then token "m" searching the stacks through B in pages of 1 finds "infra" forwards and back
    And token "m" searching the stacks where '[{"field":"label","constraint":{"stringEquals":["prod"]}}]' through A in pages of 1 finds "infra" forwards and back
    And token "m" searching the stacks where '[{"field":"space","constraint":{"hierarchyNodeValueEquals":["{team}"]}}]' through B in pages of 1 finds "" forwards and back
    When token "m" sends "POST /api/v1/stacks/search/suggestions" with '{"fields":["label","space"]}' through A
    Then the answer suggests the label values "eu:1, prod:1" of 1 stack
    And the answer suggests the space values "legacy:1" of 1 stack

  @op:searchStacks @op:searchStacksSuggestions @case:failure
  Scenario: A search of a field stacks lack, a constraint its field does not take, or an altered cursor is refused
    When token "h" sends "POST /api/v1/stacks/search" with '{"predicates":[{"field":"owner","constraint":{"stringEquals":["x"]}}]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/predicates/0/field" as "unknown-field"
    When token "h" sends "POST /api/v1/stacks/search/suggestions" with '{"predicates":[{"field":"owner","constraint":{"stringEquals":["x"]}}]}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/predicates/0/field" as "unknown-field"
    When token "h" sends "POST /api/v1/stacks/search/suggestions" with '{"predicates":[{"field":"locked","constraint":{"stringEquals":["x"]}}]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/predicates/0/constraint/stringEquals" as "rule"
    When token "h" sends "POST /api/v1/stacks/search" with '{"after":"not-a-cursor"}' through B
    Then it answers a 400 problem "invalid-cursor"
    When token "h" searches the stacks by name through A after a cursor of a search by name, cut short
    Then it answers a 400 problem "invalid-cursor"
    When token "h" searches the stacks by repository through B after a cursor of a search by name
    Then it answers a 400 problem "invalid-cursor"
