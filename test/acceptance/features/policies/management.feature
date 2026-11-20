@postgres @policy-management
Feature: Persisted policy management
  Policies belong to one account and space. A save compiles the stored body
  with the selected per-policy dialect before committing. Management does not
  imply delivery of the policy type's workflow integration.

  @op:policyCreatev2 @op:policyUpdatev2 @case:failure
  Scenario: Login policies are account-wide and cannot move into child spaces
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    When token "h" sends "POST /api/v1/policies" with '{"name":"Misplaced login","type":"LOGIN","space":"legacy","body":"package infraweave\n"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/space" as "rule"
    When token "h" sends "POST /api/v1/policies" with '{"name":"Account login","type":"LOGIN","body":"package infraweave\n"}' through B
    Then it answers 201
    And the answer's id names "login"
    When token "h" sends "PUT /api/v1/policies/{login}" with '{"name":"Moved login","space":"legacy","body":"package infraweave\n"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/space" as "rule"
    When token "h" sends "GET /api/v1/policies/{login}" through B
    Then it answers 200
    And the answer's name is '"Account login"'
    And the answer's space is '"acme"'

  @op:policyUpdatev2Patch @case:success @case:failure
  Scenario: Merge patch keeps source and dialect while resetting nullable settings
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" sends "POST /api/v1/policies" with '{"name":"Patch guard","type":"PLAN","engineType":"REGO_V0","description":"old","labels":["prod"],"body":"package infraweave\nwarn[\"old\"] { true }\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "GET /api/v1/policies/{policy}" through B
    And it answers 200
    And the answer's ETag is kept as "first"
    When token "h" sends "PATCH /api/v1/policies/{policy}" with '{"name":"Patched guard","description":null,"labels":null}' if matching the ETag "first" through A
    Then it answers 200
    And the answer's name is '"Patched guard"'
    And the answer's engineType is '"REGO_V0"'
    And the answer's labels is '[]'
    And the answer's body is '"package infraweave\nwarn[\"old\"] { true }\n"'
    When token "h" sends "PATCH /api/v1/policies/{policy}" with '{"name":"Lost write"}' if matching the ETag "first" through B
    Then it answers a 412 problem "precondition-failed"
    When token "h" sends "PATCH /api/v1/policies/{policy}" with '{"engineType":"REGO_V1"}' through B
    Then it answers a 422 problem "validation-failed"
    And the policy refusal locates the body source
    When token "h" sends "PATCH /api/v1/policies/{policy}" with '{"unknown":"value"}' through A
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "GET /api/v1/policies/{policy}" through B
    Then it answers 200
    And the answer's name is '"Patched guard"'
    And the answer's engineType is '"REGO_V0"'

  @op:policyDelete @op:policyUpdatev2 @case:success @case:failure
  Scenario: Conditional updates and deletion refuse stale policy versions across replicas
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" sends "POST /api/v1/policies" with '{"name":"Versioned","type":"PLAN","body":"package infraweave\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "GET /api/v1/policies/{policy}" through B
    And it answers 200
    And the answer's ETag is kept as "first"
    When token "h" sends "PUT /api/v1/policies/{policy}" with '{"name":"Changed","body":"package infraweave\n"}' if matching the ETag "first" through A
    Then it answers 200
    And the answer's ETag is kept as "second"
    When token "h" sends "PUT /api/v1/policies/{policy}" with '{"name":"Stale","body":"package infraweave\n"}' if matching the ETag "first" through B
    Then it answers a 412 problem "precondition-failed"
    When token "h" sends "DELETE /api/v1/policies/{policy}" if matching the ETag "first" through B
    Then it answers a 412 problem "precondition-failed"
    When token "h" sends "GET /api/v1/policies/{policy}" through A
    Then it answers 200
    And the answer's name is '"Changed"'
    When token "h" sends "DELETE /api/v1/policies/{policy}" if matching the ETag "second" through B
    Then it answers 200
    And the answer's id is '"{policy}"'
    And the answer's name is '"Changed"'
    When token "h" sends "GET /api/v1/policies/{policy}" through A
    Then it answers a 404 problem "not-found"

  @op:policyCreatev2 @op:policy @op:policyUpdatev2 @op:policyUpdatev2Patch @op:policyDelete @case:denial
  Scenario: A space reader reads its policy but cannot save policy settings
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through B for token "m"
    And the member key is space-reader on the space "legacy"
    And token "h" sends "POST /api/v1/policies" with '{"name":"Readable","type":"PLAN","space":"legacy","body":"package infraweave\n"}' through A
    And it answers 201
    And the answer's id names "readable"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Closed","description":"","inheritEntities":false,"parentSpace":"acme"}' through B
    And it answers 201
    And the answer's id is the space "closed"
    And token "h" sends "POST /api/v1/policies" with '{"name":"Hidden","type":"PLAN","space":"{closed}","body":"package infraweave\n"}' through B
    And it answers 201
    And the answer's id names "hidden"
    When token "m" sends "GET /api/v1/policies/{readable}" through B
    Then it answers 200
    When token "m" sends "GET /api/v1/policies/{hidden}" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/policies" with '{"name":"Forbidden","type":"PLAN","space":"legacy","body":"package infraweave\n"}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_ADMIN"'
    When token "m" sends "PATCH /api/v1/policies/{readable}" with '{"name":"Forbidden"}' through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "DELETE /api/v1/policies/{readable}?force=true" through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PUT /api/v1/policies/{readable}" with '{"name":"Forbidden","space":"legacy","body":"package infraweave\n"}' through A
    Then it answers a 403 problem "forbidden"
    When token "h" sends "GET /api/v1/policies/{readable}" through B
    Then it answers 200
    And the answer's name is '"Readable"'

  @op:policyCreatev2 @op:policy @op:policyUpdatev2 @case:denial
  Scenario: Switching off policies refuses creation and conceals missing policies
    Given the installation switches off the features "policies"
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    When token "h" sends "POST /api/v1/policies" with '{"name":"Disabled","type":"PLAN","body":"package infraweave\n"}' through A
    Then it answers a 403 problem "feature-disabled"
    When token "h" sends "GET /api/v1/policies/01JAAAAAAAAAAAAAAAAAAAAAAA" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "PUT /api/v1/policies/01JAAAAAAAAAAAAAAAAAAAAAAA" with '{"name":"Disabled","body":"package infraweave\n"}' through A
    Then it answers a 404 problem "not-found"

  @op:policies @op:policyCreatev2 @op:policy @op:policyUpdatev2 @op:policyUpdatev2Patch @op:policyDelete @op:searchPolicies @op:searchPoliciesSuggestions @op:searchSpacePolicies @op:searchSpacePoliciesSuggestions @case:denial
  Scenario: Revoking the policy entitlement refuses known policies on both replicas
    Given two infraweave servers, A and B, on one database, with an operator key
    And the operator key is exchanged through A for token "op"
    And the home key is exchanged through B for token "h"
    And token "h" sends "POST /api/v1/policies" with '{"name":"Existing","type":"PLAN","body":"package infraweave\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    When token "op" creates the tier "No policies" with the feature "spaces" through A
    And token "op" gives the home organization the tier "No policies" through A
    And token "op" turns billing on through A
    Then "GET /api/v1/policies/{policy}" through A with token "h" eventually answers a 403 problem "feature-not-in-tier"
    And "GET /api/v1/policies/{policy}" through B with token "h" eventually answers a 403 problem "feature-not-in-tier"
    When token "h" sends "PUT /api/v1/policies/{policy}" with '{"name":"Must not change","body":"package infraweave\n"}' through A
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "POST /api/v1/policies" with '{"name":"Must not persist","type":"PLAN","body":"package infraweave\n"}' through B
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "PATCH /api/v1/policies/{policy}" with '{"name":"Must not change"}' through B
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "DELETE /api/v1/policies/{policy}?force=true" through A
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "GET /api/v1/policies" through A
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "POST /api/v1/policies/search" with '{}' through B
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "POST /api/v1/policies/search/suggestions" with '{}' through A
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "POST /api/v1/space-policies/search" with '{"space":"acme","input":{}}' through B
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "POST /api/v1/space-policies/search/suggestions" with '{"space":"acme","input":{}}' through A
    Then it answers a 403 problem "feature-not-in-tier"

  @op:policyCreatev2 @op:policyUpdatev2 @op:spaceDelete @case:failure @case:success
  Scenario: A policy prevents deleting its owning space until it is moved
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Policy room","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And it answers 201
    And the answer's id is the space "room"
    And token "h" sends "POST /api/v1/policies" with '{"name":"Resident","type":"PLAN","space":"{room}","body":"package infraweave\n"}' through B
    And it answers 201
    And the answer's id names "policy"
    When token "h" sends "DELETE /api/v1/spaces/{room}" through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "PUT /api/v1/policies/{policy}" with '{"name":"Resident","space":"acme","body":"package infraweave\n"}' through B
    Then it answers 200
    When token "h" sends "DELETE /api/v1/spaces/{room}" through A
    Then it answers 200

  @op:policyCreatev2 @op:policyUpdatev2 @case:failure
  Scenario: Compilation refusals preserve the stored body and engine
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" sends "POST /api/v1/policies" with '{"name":"Stable guard","type":"PLAN","body":"package infraweave\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    When token "h" sends "PUT /api/v1/policies/{policy}" with '{"name":"Must not persist","engineType":"REGO_V0","body":"package elsewhere\n"}' through B
    Then it answers a 422 problem "validation-failed"
    And the policy refusal locates the body source
    When token "h" sends "GET /api/v1/policies/{policy}" through A
    Then it answers 200
    And the answer's name is '"Stable guard"'
    And the answer's engineType is '"REGO_V1"'
    And the answer's body is '"package infraweave\n"'
    When token "h" sends "POST /api/v1/policies" with '{"name":"Invalid package","type":"PLAN","body":"package elsewhere\n"}' through B
    Then it answers a 422 problem "validation-failed"
    And the policy refusal locates the body source
    When token "h" sends "PUT /api/v1/policies/{policy}" with '{"name":"Invalid engine","engineType":"REGO_V2","body":"package infraweave\n"}' through A
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "GET /api/v1/policies/{policy}" through B
    Then it answers 200
    And the answer's name is '"Stable guard"'
    And the answer's engineType is '"REGO_V1"'

  @op:policyUpdatev2 @case:success
  Scenario: An omitted engine preserves V0 while an explicit switch recompiles under V1
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" sends "POST /api/v1/policies" with '{"name":"Legacy guard","type":"PLAN","space":"acme","engineType":"REGO_V0","body":"package infraweave\nwarn[\"legacy\"] { true }\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    When token "h" sends "PUT /api/v1/policies/{policy}" with '{"name":"Renamed guard","body":"package infraweave\nwarn[\"renamed\"] { true }\n"}' through B
    Then it answers 200
    And the answer's engineType is '"REGO_V0"'
    When token "h" sends "GET /api/v1/policies/{policy}" through A
    Then it answers 200
    And the answer's name is '"Renamed guard"'
    And the answer's engineType is '"REGO_V0"'
    When token "h" sends "PUT /api/v1/policies/{policy}" with '{"name":"Upgraded guard","engineType":"REGO_V1","body":"package infraweave\nwarn contains \"upgraded\" if { true }\n"}' through B
    Then it answers 200
    And the answer's engineType is '"REGO_V1"'
    When token "h" sends "GET /api/v1/policies/{policy}" through A
    Then it answers 200
    And the answer's name is '"Upgraded guard"'
    And the answer's engineType is '"REGO_V1"'

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
