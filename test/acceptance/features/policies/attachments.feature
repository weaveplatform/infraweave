@postgres @policy-attachments
Feature: Effective policy attachments
  A stack attaches a readable policy from its own space or an inheritable
  ancestor. Its attachment has an identity distinct from the policy's identity.
  Explicit attachment wins over automatic selection and is enforced once.

  Background:
    Given two infraweave servers, A and B, on one database, with an operator key
    And the home key is exchanged through A for token "h"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"provider":"GIT","name":"Infra","repository":"infra","branch":"main","space":"acme"},"manageState":false}' through A
    And it answers 201
    And token "h" sends "POST /api/v1/policies" with '{"name":"Plan guard","type":"PLAN","body":"package infraweave\n"}' through B
    And it answers 201
    And the answer's id names "policy"

  @op:policyAttach @op:policyDetach @op:policyAttachedStack @op:attachablePoliciesInSpace @op:autoattachmentForLabels @op:stackAttachablePolicies @op:stackPolicyAttachment @case:success @case:failure
  Scenario: Account-qualified attachments, candidates and preview never cross accounts
    Given the sibling key is exchanged through B for token "s"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through A
    And it answers 200
    And the answer's id names "attachment"
    And token "s" sends "POST /api/v1/stacks" with '{"input":{"provider":"GIT","name":"Infra","repository":"infra","branch":"main","space":"acme-dev"},"manageState":false}' through B
    And it answers 201
    And token "s" sends "POST /api/v1/policies" with '{"name":"Own guard","type":"PLAN","body":"package infraweave\n","labels":["autoattach:*"]}' through A
    And it answers 201
    And the answer's id names "ownpolicy"
    When token "s" sends "GET /api/v1/attachable-policies-in-space?id=acme-dev" through A
    Then the policy page IDs are "{ownpolicy}"
    When token "s" sends "GET /api/v1/stacks/infra/attachable-policies" through B
    Then the policy page IDs are "{ownpolicy}"
    When token "s" sends "GET /api/v1/autoattachment-for-labels?space=acme-dev&labels=autoattach:*" through A
    Then it answers 200
    And the answer's count is '1'
    When token "s" sends "GET /api/v1/autoattachment-for-labels?space=acme&labels=autoattach:*" through B
    Then it answers a 404 problem "not-found"
    When token "s" sends "GET /api/v1/attachable-policies-in-space?id=acme" through A
    Then it answers a 404 problem "not-found"
    When token "s" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    Then it answers a 404 problem "not-found"
    When token "s" sends "DELETE /api/v1/policy-attachments/{attachment}" through A
    Then it answers a 404 problem "not-found"
    When token "s" sends "GET /api/v1/policies/{policy}/attached-stack?id={attachment}" through B
    Then it answers a 404 problem "not-found"
    When token "s" sends "GET /api/v1/stacks/infra/policy-attachment?id={attachment}" through A
    Then it answers a 404 problem "not-found"
    When token "s" sends "GET /api/v1/stacks/infra" through B
    Then the stack's attached policy IDs are "{ownpolicy}"
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then the stack's attached policy IDs are "{policy}"

  @op:attachablePoliciesInSpace @op:autoattachmentForLabels @op:stackAttachablePolicies @op:stackPolicyAttachment @case:success @case:failure
  Scenario: A child reader inherits ancestor reads without seeing sibling targets
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Child","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And it answers 201
    And the answer's id is the space "child"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"provider":"GIT","name":"Child stack","repository":"infra","branch":"main","space":"{child}"},"manageState":false}' through B
    And it answers 201
    And the answer's id names "childstack"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Hidden sibling","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And it answers 201
    And the answer's id is the space "hidden"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"provider":"GIT","name":"Hidden stack","repository":"infra","branch":"main","space":"{hidden}"},"manageState":false}' through B
    And it answers 201
    And the answer's id names "hiddenstack"
    And token "h" sends "PUT /api/v1/policies/{policy}" with '{"name":"Plan guard","body":"package infraweave\n","labels":["autoattach:*"]}' through A
    And it answers 200
    And the policy's attachment to "{hiddenstack}" is automatic and kept as "hiddenattachment"
    And token "h" sends "POST /api/v1/policies" with '{"name":"Child guard","type":"PLAN","body":"package infraweave\n","space":"{child}","labels":["autoattach:*"]}' through B
    And it answers 201
    And the answer's id names "childpolicy"
    And the member key is exchanged through A for token "m"
    And the member key is space-reader on the space "{child}"
    When token "m" sends "GET /api/v1/attachable-policies-in-space?id={child}" through B
    Then the policy page IDs are "{childpolicy}, {policy}"
    When token "m" sends "GET /api/v1/stacks/{childstack}/attachable-policies" through A
    Then the policy page IDs are "{childpolicy}, {policy}"
    When token "m" sends "GET /api/v1/autoattachment-for-labels?space={child}&labels=autoattach:*" through B
    Then it answers 200
    And the answer's count is '1'
    When token "m" sends "GET /api/v1/autoattachment-for-labels?space=acme&labels=autoattach:*" through A
    Then it answers 200
    And the answer's count is '2'
    When token "m" sends "GET /api/v1/autoattachment-for-labels?space={hidden}&labels=autoattach:*" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/stacks/{childstack}" through B
    Then the stack's attached policy IDs are "{childpolicy}, {policy}"
    When token "m" sends "GET /api/v1/stacks/{hiddenstack}" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/policies/{policy}/attached-stack?id={hiddenattachment}" through B
    Then it answers a 404 problem "not-found"

  @op:policyAttach @op:policyDetach @case:failure
  Scenario: Legacy stack policy actions remain disabled for custom role assignment
    When token "h" sends "POST /api/v1/roles" with '{"name":"Legacy attach","actions":["STACK_POLICY_ATTACH"]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/actions/0" naming "STACK_POLICY_ATTACH"
    When token "h" sends "POST /api/v1/roles" with '{"name":"Legacy detach","actions":["STACK_POLICY_DETACH"]}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/actions/0" naming "STACK_POLICY_DETACH"

  @op:policyAttach @op:stackPolicyAttachment @case:success
  Scenario: Concurrent automatic selection and explicit promotion evaluate one effective policy
    When explicit and automatic attachments race with label writes on both replicas
    Then the selected policy collection evaluates exactly once
    And policy "{policy}" eventually has 1 stored attachments
    When token "h" sends "GET /api/v1/stacks/infra/policy-attachment?id={attachment}" through A
    Then it answers 200
    And the answer's isAutoattached is 'false'

  @op:policyAttach @op:policyDetach @op:policyAttachedStack @op:attachablePoliciesInSpace @op:autoattachmentForLabels @op:stackAttachablePolicies @op:stackPolicyAttachment @case:denial
  Scenario: Revoking the policy entitlement refuses every attachment operation
    Given the operator key is exchanged through B for token "op"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through A
    And it answers 200
    And the answer's id names "attachment"
    When token "op" creates the tier "No policies" with the feature "spaces" through A
    And token "op" gives the home organization the tier "No policies" through A
    And token "op" turns billing on through A
    Then "GET /api/v1/attachable-policies-in-space?id=acme" through A with token "h" eventually answers a 403 problem "feature-not-in-tier"
    And "GET /api/v1/autoattachment-for-labels?space=acme&labels=autoattach:*" through B with token "h" eventually answers a 403 problem "feature-not-in-tier"
    When token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "DELETE /api/v1/policy-attachments/{attachment}" through A
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "GET /api/v1/policies/{policy}/attached-stack?id={attachment}" through B
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "GET /api/v1/stacks/infra/attachable-policies" through A
    Then it answers a 403 problem "feature-not-in-tier"
    When token "h" sends "GET /api/v1/stacks/infra/policy-attachment?id={attachment}" through B
    Then it answers a 403 problem "feature-not-in-tier"

  @op:attachablePoliciesInSpace @op:stackAttachablePolicies @op:policyAttach @op:policyDetach @op:policyAttachedStack @op:stackPolicyAttachment @case:failure
  Scenario: Missing, malformed and mismatched attachment parents are refused
    When token "h" sends "GET /api/v1/attachable-policies-in-space?id=missing" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/stacks/missing/attachable-policies" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/attachable-policies-in-space?id=acme&cursor=invalid" through A
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "GET /api/v1/stacks/infra/attachable-policies?cursor=invalid" through B
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "PUT /api/v1/stacks/missing/policies/{policy}" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "PUT /api/v1/stacks/infra/policies/invalid" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "DELETE /api/v1/policy-attachments/invalid" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/policies/{policy}/attached-stack?id=invalid" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/stacks/infra/policy-attachment?id=invalid" through A
    Then it answers a 404 problem "not-found"

  @op:policyAttach @op:policyDetach @op:policyAttachedStack @case:success @case:failure
  Scenario: Explicit attachment guards inheritance and moves of either endpoint
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Child","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And it answers 201
    And the answer's id is the space "child"
    And token "h" sends "PUT /api/v1/stacks/infra" with '{"provider":"GIT","name":"Infra","repository":"infra","branch":"main","space":"{child}"}' through B
    And it answers 200
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through A
    And it answers 200
    And the answer's id names "attachment"
    When token "h" sends "PUT /api/v1/spaces/{child}" with '{"name":"Child","description":"","inheritEntities":false,"parentSpace":"acme"}' through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "PUT /api/v1/spaces/{child}" with '{"name":"Child","description":"","inheritEntities":true,"parentSpace":"legacy"}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "PUT /api/v1/policies/{policy}" with '{"name":"Plan guard","body":"package infraweave\n","space":"legacy"}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "PUT /api/v1/stacks/infra" with '{"provider":"GIT","name":"Infra","repository":"infra","branch":"main","space":"legacy"}' through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "GET /api/v1/policies/{policy}/attached-stack?id={attachment}" through B
    Then it answers 200
    When token "h" sends "DELETE /api/v1/policy-attachments/{attachment}" through A
    Then it answers 200
    When token "h" sends "PUT /api/v1/spaces/{child}" with '{"name":"Child","description":"","inheritEntities":false,"parentSpace":"acme"}' through B
    Then it answers 200

  @op:autoattachmentForLabels @op:stackPolicyAttachment @case:success
  Scenario: Automatic attachment follows accepted inheritance and policy space changes
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Child","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And it answers 201
    And the answer's id is the space "child"
    And token "h" sends "PUT /api/v1/stacks/infra" with '{"provider":"GIT","name":"Infra","repository":"infra","branch":"main","space":"{child}"}' through B
    And it answers 200
    When token "h" sends "PUT /api/v1/policies/{policy}" with '{"name":"Plan guard","body":"package infraweave\n","labels":["autoattach:*"]}' through A
    Then it answers 200
    And the answer's hasAttachedStacks is 'true'
    And policy "{policy}" eventually has 1 stored attachments
    When token "h" sends "PUT /api/v1/spaces/{child}" with '{"name":"Child","description":"","inheritEntities":false,"parentSpace":"acme"}' through B
    Then it answers 200
    And policy "{policy}" through A with token "h" eventually has attachments '{"hasAttachedStacks":false,"attachedStacks":[]}'
    And policy "{policy}" eventually has 0 stored attachments
    When token "h" sends "GET /api/v1/autoattachment-for-labels?space=acme&labels=autoattach:*" through A
    Then it answers 200
    And the answer's count is '0'
    When token "h" sends "PUT /api/v1/spaces/{child}" with '{"name":"Child","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    Then it answers 200
    And policy "{policy}" through B with token "h" eventually has attachments '{"hasAttachedStacks":true}'
    And policy "{policy}" eventually has 1 stored attachments
    When token "h" sends "PUT /api/v1/spaces/{child}" with '{"name":"Child","description":"","inheritEntities":true,"parentSpace":"legacy"}' through B
    Then it answers 200
    And policy "{policy}" eventually has 0 stored attachments
    When token "h" sends "PUT /api/v1/spaces/{child}" with '{"name":"Child","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    Then it answers 200
    And policy "{policy}" eventually has 1 stored attachments
    When token "h" sends "PUT /api/v1/policies/{policy}" with '{"name":"Plan guard","body":"package infraweave\n","space":"legacy","labels":["autoattach:*"]}' through B
    Then it answers 200
    And the answer's hasAttachedStacks is 'false'
    And policy "{policy}" eventually has 0 stored attachments
    When token "h" sends "DELETE /api/v1/stacks/infra" through A
    Then it answers 200
    And policy "{policy}" eventually has 0 stored attachments

  @op:stackPolicyAttachment @case:failure @case:success
  Scenario: A failed durable reconcile request refuses the space change atomically
    Given token "h" sends "POST /api/v1/spaces" with '{"name":"Child","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And it answers 201
    And the answer's id is the space "child"
    And token "h" sends "PUT /api/v1/stacks/infra" with '{"provider":"GIT","name":"Infra","repository":"infra","branch":"main","space":"{child}"}' through B
    And it answers 200
    And token "h" sends "PUT /api/v1/policies/{policy}" with '{"name":"Plan guard","body":"package infraweave\n","labels":["autoattach:*"]}' through A
    And it answers 200
    And policy "{policy}" eventually has 1 stored attachments
    And policy reconciliation storage fails
    When token "h" sends "PUT /api/v1/spaces/{child}" with '{"name":"Child","description":"","inheritEntities":false,"parentSpace":"acme"}' through B
    Then it answers a 503 problem "service-unavailable"
    When token "h" sends "GET /api/v1/spaces/{child}" through A
    Then it answers 200
    And the answer's inheritEntities is 'true'
    And policy "{policy}" eventually has 1 stored attachments
    Given policy reconciliation storage recovers
    When token "h" sends "PUT /api/v1/spaces/{child}" with '{"name":"Child","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    Then it answers 200
    And policy "{policy}" eventually has 0 stored attachments

  @op:autoattachmentForLabels @case:success @case:failure
  Scenario: Preview counts all matching targets and returns current target labels
    When token "h" sends "GET /api/v1/autoattachment-for-labels?space=acme&labels=ordinary" through B
    Then it answers 200
    And the answer's count is '0'
    And the answer's sample is '[]'
    When token "h" sends "GET /api/v1/autoattachment-for-labels?space=acme&labels=autoattach:*" through A
    Then it answers 200
    And the answer's count is '1'
    And the answer's sample is '[{"id":"infra","isModule":false,"labels":[],"name":"Infra","space":"acme","spaceDetails":{"id":"acme"}}]'
    When token "h" sends "GET /api/v1/autoattachment-for-labels?space=missing&labels=autoattach:*" through B
    Then it answers a 404 problem "not-found"

  @op:stackPolicyAttachment @op:policyDetach @case:success
  Scenario: Stack and policy labels reconcile automatic attachments across replicas
    When token "h" sends "PUT /api/v1/policies/{policy}" with '{"name":"Plan guard","body":"package infraweave\n","labels":["autoattach:prod"]}' through A
    Then it answers 200
    And the answer's hasAttachedStacks is 'false'
    When token "h" sends "PUT /api/v1/stacks/infra" with '{"provider":"GIT","name":"Infra","repository":"infra","branch":"main","space":"acme","labels":["prod"]}' through B
    Then it answers 200
    And policy "{policy}" through A with token "h" eventually has attachments '{"hasAttachedStacks":true}'
    And the policy's attachment to "infra" is automatic and kept as "attachment"
    When token "h" sends "DELETE /api/v1/policy-attachments/{attachment}" through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "PUT /api/v1/stacks/infra" with '{"provider":"GIT","name":"Infra","repository":"infra","branch":"main","space":"acme","labels":[]}' through A
    Then it answers 200
    And policy "{policy}" through B with token "h" eventually has attachments '{"hasAttachedStacks":false,"attachedStacks":[]}'
    And policy "{policy}" eventually has 0 stored attachments
    When token "h" sends "PUT /api/v1/policies/{policy}" with '{"name":"Plan guard","body":"package infraweave\n","labels":["autoattach:*"]}' through B
    Then it answers 200
    And the answer's hasAttachedStacks is 'true'
    When token "h" sends "PUT /api/v1/policies/{policy}" with '{"name":"Plan guard","body":"package infraweave\n","labels":[]}' through A
    Then it answers 200
    And the answer's hasAttachedStacks is 'false'

  @op:policyAttach @op:policyDetach @case:success
  Scenario: Explicit attachment replaces the automatic marker and survives label removal
    When token "h" sends "PUT /api/v1/policies/{policy}" with '{"name":"Plan guard","body":"package infraweave\n","labels":["autoattach:*"]}' through A
    Then it answers 200
    And the answer's hasAttachedStacks is 'true'
    And the policy's attachment to "infra" is automatic and kept as "attachment"
    When token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    Then it answers 200
    And the answer's id is '"{attachment}"'
    And the answer's isAutoattached is 'false'
    When token "h" sends "PUT /api/v1/policies/{policy}" with '{"name":"Plan guard","body":"package infraweave\n","labels":[]}' through A
    Then it answers 200
    And the answer's hasAttachedStacks is 'true'
    When token "h" sends "GET /api/v1/stacks/infra/policy-attachment?id={attachment}" through B
    Then it answers 200
    And the answer's isAutoattached is 'false'
    When token "h" sends "DELETE /api/v1/policy-attachments/{attachment}" through A
    Then it answers 200
    When token "h" sends "GET /api/v1/policies/{policy}" through B
    Then it answers 200
    And the answer's hasAttachedStacks is 'false'

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
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Account login","type":"LOGIN","body":"package infraweave\n","labels":["autoattach:*"]}' through A
    And it answers 201
    And the answer's hasAttachedStacks is 'false'
    And the answer's id names "login"
    And token "h" sends "POST /api/v1/policies" with '{"name":"Notifications","type":"NOTIFICATION","body":"package infraweave\n","labels":["autoattach:*"]}' through B
    And it answers 201
    And the answer's hasAttachedStacks is 'false'
    And the answer's id names "notification"
    And token "h" sends "POST /api/v1/policies" with '{"name":"Intent guard","type":"INTENT","body":"package infraweave\n","labels":["autoattach:*"]}' through A
    And it answers 201
    And the answer's hasAttachedStacks is 'false'
    And the answer's id names "intent"
    When token "h" sends "GET /api/v1/attachable-policies-in-space?id=acme" through B
    Then the policy page IDs are "{policy}"
    When token "h" sends "GET /api/v1/stacks/infra/attachable-policies" through A
    Then the policy page IDs are "{policy}"
    When token "h" sends "PUT /api/v1/stacks/infra/policies/{login}" through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "PUT /api/v1/stacks/infra/policies/{notification}" through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "PUT /api/v1/stacks/infra/policies/{intent}" through B
    Then it answers a 422 problem "validation-failed"

  @op:stackPolicyAttachment @case:success
  Scenario: Git push policies participate in automatic stack label reconciliation
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Push guard","type":"GIT_PUSH","body":"package infraweave\n","labels":["autoattach:prod"]}' through A
    And it answers 201
    And the answer's id names "push"
    And the answer's hasAttachedStacks is 'false'
    When token "h" sends "PUT /api/v1/stacks/infra" with '{"provider":"GIT","name":"Infra","repository":"infra","branch":"main","space":"acme","labels":["prod"]}' through B
    Then it answers 200
    And policy "{push}" through A with token "h" eventually has attachments '{"hasAttachedStacks":true}'
    And policy "{push}" eventually has 1 stored attachments
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then the stack's attached policy IDs are "{push}"

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
