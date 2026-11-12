@postgres
Feature: A PUT replaces an entity and a PATCH changes what it names
  A PUT sends an entity's whole input: a field it leaves out takes its
  default, and a null sets a nullable field to null. A PATCH sends a JSON
  Merge Patch: it changes the fields it names and keeps the rest, a null
  resets a field to its default, and an array replaces the old one whole.
  Every read and write of one entity answers its ETag. A PUT, PATCH or
  DELETE whose If-Match names a version the entity has moved past answers
  412 and changes nothing. A PUT to an item's own path creates it, 201,
  or replaces it, 200. The home key is space-admin on its account's top
  space.

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"

  @op:stackCreate @op:stackPatch @op:stackUpdate @case:success
  Scenario: On a stack, a PATCH keeps what it leaves out and a PUT resets it
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy","description":"ours","labels":["a","b"],"autodeploy":true,"githubActionDeploy":false},"manageState":false}' through A
    When token "h" sends "PATCH /api/v1/stacks/infra" with '{"branch":"dev"}' through B
    Then it answers 200
    And the answer's branch is '"dev"'
    And the answer's description is '"ours"'
    And the answer's autodeploy is 'true'
    And the answer's labels is '["a","b"]'
    When token "h" sends "PATCH /api/v1/stacks/infra" with '{"labels":["c"],"autodeploy":null,"githubActionDeploy":null}' through A
    Then it answers 200
    And the answer's labels is '["c"]'
    And the answer's autodeploy is 'false'
    And the answer's githubActionDeploy is 'true'
    And the answer's branch is '"dev"'
    When token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"main","space":"legacy","description":null}' through B
    Then it answers 200
    And the answer leaves out "ours"
    And the answer's labels is '[]'
    And the answer's githubActionDeploy is 'true'

  @op:spaceCreate @op:spacePatch @op:spaceUpdate @case:success
  Scenario: On a space, a PATCH keeps what it leaves out and a PUT resets it
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"ours","inheritEntities":true,"parentSpace":"acme","labels":["a","b"]}' through A
    And the answer's id is the space "team"
    When token "h" sends "PATCH /api/v1/spaces/{team}" with '{"name":"Team Two"}' through B
    Then it answers 200
    And the answer's name is '"Team Two"'
    And the answer's description is '"ours"'
    And the answer's inheritEntities is 'true'
    And the answer's labels is '["a","b"]'
    When token "h" sends "PATCH /api/v1/spaces/{team}" with '{"labels":["c"]}' through A
    Then it answers 200
    And the answer's labels is '["c"]'
    When token "h" sends "PATCH /api/v1/spaces/{team}" with '{"labels":null}' through B
    Then it answers 200
    And the answer's labels is '[]'
    When token "h" sends "PUT /api/v1/spaces/{team}" with '{"name":"Team Two","description":"","inheritEntities":false,"parentSpace":"acme","labels":["d"]}' through A
    Then it answers 200
    When token "h" sends "PUT /api/v1/spaces/{team}" with '{"name":"Team Two","description":"","inheritEntities":false,"parentSpace":"acme","labels":null}' through B
    Then it answers 200
    And the answer's labels is '[]'

  @op:stack @op:stackUpdate @op:stackPatch @op:stackDelete @case:failure @case:success
  Scenario: On a stack, an If-Match of a version since changed answers 412
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through A
    And token "h" sends "GET /api/v1/stacks/infra" through B
    And the answer's ETag is kept as "first"
    When token "h" sends "PATCH /api/v1/stacks/infra" with '{"branch":"dev"}' if matching the ETag "first" through A
    Then it answers 200
    And the answer's ETag is kept as "second"
    When token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"main","space":"legacy"}' if matching the ETag "first" through B
    Then it answers a 412 problem "precondition-failed"
    When token "h" sends "PATCH /api/v1/stacks/infra" with '{"branch":"main"}' if matching the ETag "first" through A
    Then it answers a 412 problem "precondition-failed"
    When token "h" sends "DELETE /api/v1/stacks/infra" if matching the ETag "first" through B
    Then it answers a 412 problem "precondition-failed"
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then the answer's branch is '"dev"'
    When token "h" sends "DELETE /api/v1/stacks/infra" if matching the ETag "second" through B
    Then it answers 200

  @op:space @op:spaceUpdate @op:spacePatch @op:spaceDelete @case:failure @case:success
  Scenario: On a space, an If-Match of a version since changed answers 412
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And token "h" sends "GET /api/v1/spaces/{team}" through B
    And the answer's ETag is kept as "first"
    When token "h" sends "PATCH /api/v1/spaces/{team}" with '{"description":"ours"}' if matching the ETag "first" through A
    Then it answers 200
    And the answer's ETag is kept as "second"
    When token "h" sends "PUT /api/v1/spaces/{team}" with '{"name":"Team","description":"","inheritEntities":false,"parentSpace":"acme"}' if matching the ETag "first" through B
    Then it answers a 412 problem "precondition-failed"
    When token "h" sends "PATCH /api/v1/spaces/{team}" with '{"description":""}' if matching the ETag "first" through A
    Then it answers a 412 problem "precondition-failed"
    When token "h" sends "DELETE /api/v1/spaces/{team}" if matching the ETag "first" through B
    Then it answers a 412 problem "precondition-failed"
    When token "h" sends "GET /api/v1/spaces/{team}" through A
    Then the answer's description is '"ours"'
    When token "h" sends "DELETE /api/v1/spaces/{team}" if matching the ETag "second" through B
    Then it answers 200

  @op:stackConfigSet @case:success
  Scenario: A stack's config element PUT at its own path creates it, then replaces it
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through A
    When token "h" sends "PUT /api/v1/stacks/infra/configs/REGION" with '{"type":"ENVIRONMENT_VARIABLE","value":"eu","writeOnly":false}' through B
    Then it answers 201
    And the answer's value is '"eu"'
    When token "h" sends "PUT /api/v1/stacks/infra/configs/REGION" with '{"type":"ENVIRONMENT_VARIABLE","value":"us","writeOnly":false}' through A
    Then it answers 200
    And the answer's value is '"us"'
