@postgres @policy-attachments
Feature: Effective policy attachments
  A stack attaches a readable policy from its own space or an inheritable
  ancestor. Its attachment has an identity distinct from the policy's identity.
  Explicit attachment wins over automatic selection and is enforced once.

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"provider":"GIT","name":"Infra","repository":"infra","branch":"main","space":"acme"},"manageState":false}' through A
    And it answers 201
    And token "h" sends "POST /api/v1/policies" with '{"name":"Plan guard","type":"PLAN","body":"package infraweave\n"}' through B
    And it answers 201
    And the answer's id names "policy"

  @op:policyAttach @op:policyDetach @op:policyAttachedStack @op:stackPolicyAttachment @case:success @case:failure
  Scenario: Explicit attachment persists once with distinct policy and attachment IDs
    When token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through A
    Then it answers 200
    And the answer's id names "attachment"
    And the policy and attachment have distinct ULIDs
    And the answer's stackId is '"infra"'
    And the answer's isAutoattached is 'false'
    When token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    Then it answers 200
    And the answer's id is '"{attachment}"'
    When token "h" sends "GET /api/v1/policies/{policy}/attached-stack?id={attachment}" through B
    Then it answers 200
    And the answer's id is '"{attachment}"'
    And the answer's stackId is '"infra"'
    When token "h" sends "GET /api/v1/stacks/infra/policy-attachment?id={attachment}" through A
    Then it answers 200
    And the answer's id is '"{attachment}"'
    And the answer's policyId is '"{policy}"'
    And the answer's policyName is '"Plan guard"'
    When token "h" sends "GET /api/v1/policies/{policy}" through A
    Then it answers 200
    And the answer's hasAttachedStacks is 'true'
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then it answers 200
    And the answer's attachedPolicies is '[{"id":"{attachment}","isAutoattached":false,"policyId":"{policy}","policyName":"Plan guard","policyType":"PLAN","policyBody":"package infraweave\n","policyLabels":[],"evaluationRecords":[]}]'
    When token "h" sends "DELETE /api/v1/policy-attachments/{attachment}" through B
    Then it answers 200
    And the answer's id is '"{attachment}"'
    When token "h" sends "GET /api/v1/policies/{policy}/attached-stack?id={attachment}" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/stacks/infra/policy-attachment?id={attachment}" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/policies/{policy}" through A
    Then it answers 200
    And the answer's hasAttachedStacks is 'false'
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then it answers 200
    And the answer's attachedPolicies is '[]'

  @op:policyAttach @op:policyDetach @op:policyAttachedStack @op:stackPolicyAttachment @op:policyDelete @case:success @case:failure
  Scenario: C9 forced deletion removes the policy and both attachment projections
    Given token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through A
    And it answers 200
    And the answer's id names "attachment"
    When token "h" sends "DELETE /api/v1/policies/{policy}" through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "GET /api/v1/policies/{policy}" through A
    Then it answers 200
    And the answer's hasAttachedStacks is 'true'
    When token "h" sends "GET /api/v1/stacks/infra/policy-attachment?id={attachment}" through B
    Then it answers 200
    And the answer's id is '"{attachment}"'
    When token "h" sends "DELETE /api/v1/policies/{policy}?force=true" through B
    Then it answers 200
    And the answer's id is '"{policy}"'
    When token "h" sends "GET /api/v1/policies/{policy}" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/stacks/infra/policy-attachment?id={attachment}" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then it answers 200
    And the answer's attachedPolicies is '[]'

  @op:policyAttach @op:policyDetach @op:policyAttachedStack @op:stackPolicyAttachment @case:denial
  Scenario: A space reader reads attachments and cannot invoke the legacy admin fallbacks
    Given the member key is exchanged through B for token "m"
    And the member key is space-reader on the space "acme"
    When token "m" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_POLICY_ATTACH"'
    When token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through A
    Then it answers 200
    And the answer's id names "attachment"
    When token "m" sends "GET /api/v1/policies/{policy}/attached-stack?id={attachment}" through B
    Then it answers 200
    When token "m" sends "GET /api/v1/stacks/infra/policy-attachment?id={attachment}" through A
    Then it answers 200
    And the answer's policyId is '"{policy}"'
    When token "m" sends "DELETE /api/v1/policy-attachments/{attachment}" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_POLICY_DETACH"'
    When token "h" sends "GET /api/v1/policies/{policy}/attached-stack?id={attachment}" through A
    Then it answers 200
    And the answer's id is '"{attachment}"'

  @op:attachablePoliciesInSpace @op:stackAttachablePolicies @case:success
  Scenario: Candidate lists exclude policy types that do not attach to stacks
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Account login","type":"LOGIN","body":"package infraweave\n"}' through A
    And it answers 201
    And token "h" sends "POST /api/v1/policies" with '{"name":"Notifications","type":"NOTIFICATION","body":"package infraweave\n"}' through B
    And it answers 201
    And token "h" sends "POST /api/v1/policies" with '{"name":"Intent guard","type":"INTENT","body":"package infraweave\n"}' through A
    And it answers 201
    When token "h" sends "GET /api/v1/attachable-policies-in-space?id=acme" through B
    Then the policy page IDs are "{policy}"
    When token "h" sends "GET /api/v1/stacks/infra/attachable-policies" through A
    Then the policy page IDs are "{policy}"

  @op:attachablePoliciesInSpace @op:stackAttachablePolicies @op:policyAttach @case:success @case:failure
  Scenario: Candidates and explicit attachment follow the same inheritance boundary
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Child","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And it answers 201
    And the answer's id is the space "child"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Boundary","description":"","inheritEntities":false,"parentSpace":"acme"}' through B
    And it answers 201
    And the answer's id is the space "boundary"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"provider":"GIT","name":"Child stack","repository":"infra","branch":"main","space":"{child}"},"manageState":false}' through A
    And it answers 201
    And the answer's id names "childstack"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"provider":"GIT","name":"Boundary stack","repository":"infra","branch":"main","space":"{boundary}"},"manageState":false}' through B
    And it answers 201
    And the answer's id names "boundarystack"
    And token "h" sends "POST /api/v1/policies" with '{"name":"Child guard","type":"PLAN","space":"{child}","body":"package infraweave\n"}' through A
    And it answers 201
    And the answer's id names "childpolicy"
    And token "h" sends "POST /api/v1/policies" with '{"name":"Boundary guard","type":"PLAN","space":"{boundary}","body":"package infraweave\n"}' through B
    And it answers 201
    And the answer's id names "boundarypolicy"
    When token "h" sends "GET /api/v1/attachable-policies-in-space?id={child}" through B
    Then the policy page IDs are "{childpolicy}, {policy}"
    When token "h" sends "GET /api/v1/stacks/{childstack}/attachable-policies" through A
    Then the policy page IDs are "{childpolicy}, {policy}"
    When token "h" sends "GET /api/v1/attachable-policies-in-space?id={boundary}" through A
    Then the policy page IDs are "{boundarypolicy}"
    When token "h" sends "GET /api/v1/stacks/{boundarystack}/attachable-policies" through B
    Then the policy page IDs are "{boundarypolicy}"
    When token "h" sends "PUT /api/v1/stacks/{boundarystack}/policies/{policy}" through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "PUT /api/v1/stacks/infra/policies/{childpolicy}" through B
    Then it answers a 422 problem "validation-failed"
