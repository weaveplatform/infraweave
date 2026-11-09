@postgres
Feature: Contexts
  A context lives in a space and holds config elements, environment
  variables and mounted files, whose values are sealed at rest; a
  write-only value is never answered again, only its checksum. A stack
  attaches the contexts of its own space and of the spaces it reaches
  while each inherits, and neither the stack nor the context moves, nor a
  space between them, so as to leave the other out of reach. A context
  attached to a stack is deleted only when forced. Account admins,
  space-admin on the top space, create, change and delete contexts and
  attach them; a space reader reads them. A context in a space the caller
  cannot read answers as one that does not exist. The home key is
  space-admin on the account's top space; the member key holds only the
  bindings a scenario gives it. Team inherits from acme, child inherits
  from team, and stack infra lives in child.

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Child","description":"","inheritEntities":true,"parentSpace":"{team}"}' through B
    And the answer's id is the space "child"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"{child}"},"manageState":false}' through A

  @op:contextCreateV2 @op:context @op:contexts @op:contextUpdateV2 @op:contextPatch @op:contextDelete @op:contextConfigElement @case:success
  Scenario: An account admin creates a context attached to a stack, reads, changes and deletes it
    When token "h" sends "POST /api/v1/contexts" with '{"name":"Shared","space":"{team}","labels":["b","a"],"configAttachments":[{"id":"REGION","type":"ENVIRONMENT_VARIABLE","value":"eu-west-1","writeOnly":false},{"id":"TOKEN","type":"ENVIRONMENT_VARIABLE","value":"s3cret","writeOnly":true}],"stackAttachments":[{"stack":"infra","priority":2}]}' through A
    Then it answers 201
    And the answer's id is '"shared"'
    And the answer's space is '"{team}"'
    And the answer's labels is '["a","b"]'
    And the answer's hasAttachedStacks is 'true'
    When token "h" sends "GET /api/v1/contexts/shared/config-element?id=TOKEN" through B
    Then it answers 200
    And the answer's checksum is '"1ec1c26b50d5d3c58d9583181af8076655fe00756bf7285940ba3670f99fcba0"'
    And the answer leaves out "value"
    When token "h" sends "GET /api/v1/contexts/shared/config-element?id=REGION" through A
    Then the answer's value is '"eu-west-1"'
    When token "h" sends "GET /api/v1/contexts" through B
    Then the answer lists the contexts "shared"
    When token "h" sends "PUT /api/v1/contexts/shared" with '{"input":{"name":"Shared Two","space":"{team}","configAttachments":[{"id":"ZONE","type":"ENVIRONMENT_VARIABLE","value":"a","writeOnly":false}]},"replaceConfigElements":true}' through A
    Then it answers 200
    And the answer's id is '"shared"'
    And the answer's name is '"Shared Two"'
    And the answer's labels is '[]'
    When token "h" sends "GET /api/v1/contexts/shared/config-element?id=TOKEN" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "PATCH /api/v1/contexts/shared" with '{"input":{"description":"ours"}}' through B
    Then it answers 200
    And the answer's description is '"ours"'
    And the answer's name is '"Shared Two"'
    When token "h" sends "DELETE /api/v1/contexts/shared?force=true" through A
    Then it answers 200
    When token "h" sends "GET /api/v1/contexts/shared" through B
    Then it answers a 404 problem "not-found"

  @op:contextConfigAdd @op:contextConfigSet @op:contextConfigDelete @op:contextConfigElement @op:contextAttach @op:contextDetach @op:contextAttachedStack @case:success
  Scenario: A context's elements are added, set and deleted, and its attachments made, changed and removed
    Given token "h" sends "POST /api/v1/contexts" with '{"name":"Shared","space":"{team}"}' through A
    When token "h" sends "POST /api/v1/contexts/shared/configs" with '{"id":"conf/app.toml","type":"FILE_MOUNT","value":"a2V5ID0gMQo=","writeOnly":false,"fileMode":"600"}' through B
    Then it answers 201
    And the answer's fileMode is '"600"'
    When token "h" sends "POST /api/v1/contexts/shared/configs" with '{"id":"conf/app.toml","type":"FILE_MOUNT","value":"a2V5ID0gMgo=","writeOnly":false}' through A
    Then it answers 200
    And the answer's value is '"a2V5ID0gMgo="'
    When token "h" sends "PUT /api/v1/contexts/shared/configs/TOKEN" with '{"type":"ENVIRONMENT_VARIABLE","value":"s3cret","writeOnly":true}' through B
    Then it answers 201
    When token "h" sends "PUT /api/v1/contexts/shared/configs/TOKEN" with '{"type":"ENVIRONMENT_VARIABLE","writeOnly":true,"description":"rotated"}' through A
    Then it answers 200
    And the answer's checksum is '"1ec1c26b50d5d3c58d9583181af8076655fe00756bf7285940ba3670f99fcba0"'
    And the answer's description is '"rotated"'
    And the answer leaves out "value"
    When token "h" sends "DELETE /api/v1/contexts/shared/configs/TOKEN" through B
    Then it answers 200
    When token "h" sends "GET /api/v1/contexts/shared/config-element?id=TOKEN" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "PUT /api/v1/stacks/infra/contexts/shared" with '{"priority":3}' through B
    Then it answers 200
    And the answer's priority is '3'
    And the answer's stackId is '"infra"'
    And the answer's id names "att"
    When token "h" sends "PUT /api/v1/stacks/infra/contexts/shared" with '{"priority":1}' through A
    Then the answer's id is '"{att}"'
    And the answer's priority is '1'
    When token "h" sends "GET /api/v1/contexts/shared/attached-stack?id={att}" through B
    Then it answers 200
    And the answer's stackName is '"Infra"'
    When token "h" sends "DELETE /api/v1/context-attachments/{att}" through A
    Then it answers 200
    When token "h" sends "GET /api/v1/contexts/shared/attached-stack?id={att}" through B
    Then it answers a 404 problem "not-found"

  @op:searchContexts @op:searchContextsSuggestions @op:attachableContextsInSpace @op:searchSpaceContexts @op:searchSpaceContextsSuggestions @case:success
  Scenario: Searches find the contexts the caller reads, and those a stack in a space may attach
    Given token "h" sends "POST /api/v1/contexts" with '{"name":"Top","space":"acme","labels":["eu"]}' through A
    And token "h" sends "POST /api/v1/contexts" with '{"name":"Ours","space":"{team}","labels":["eu","prod"]}' through B
    And token "h" sends "POST /api/v1/contexts" with '{"name":"Other","space":"legacy"}' through A
    When token "h" sends "POST /api/v1/contexts/search" with '{}' through B
    Then the answer lists the contexts "other, ours, top"
    When token "h" sends "POST /api/v1/contexts/search/suggestions" with '{"fields":["label"]}' through A
    Then the answer suggests the label values "eu:2, prod:1" of 3 contexts
    When token "h" sends "GET /api/v1/attachable-contexts-in-space?id={child}" through B
    Then the answer lists the contexts "ours, top"
    When token "h" sends "POST /api/v1/space-contexts/search" with '{"space":"{child}","input":{}}' through A
    Then the answer lists the contexts "ours, top"
    When token "h" sends "POST /api/v1/space-contexts/search/suggestions" with '{"space":"{child}","input":{"fields":["label"]}}' through B
    Then the answer suggests the label values "eu:2, prod:1" of 2 contexts

  @op:contexts @op:context @op:contextCreateV2 @op:contextUpdateV2 @op:contextPatch @op:contextDelete @op:contextConfigAdd @op:contextConfigSet @op:contextConfigDelete @op:contextConfigElement @op:contextAttach @op:contextDetach @op:contextAttachedStack @op:searchContexts @op:searchContextsSuggestions @op:attachableContextsInSpace @op:searchSpaceContexts @op:searchSpaceContextsSuggestions @case:denial
  Scenario: A space reader reads the contexts of its spaces alone and changes none
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Web","repository":"web","branch":"main","space":"legacy"},"manageState":false}' through A
    And token "h" sends "POST /api/v1/contexts" with '{"name":"Open","space":"legacy","configAttachments":[{"id":"A","type":"ENVIRONMENT_VARIABLE","value":"1","writeOnly":false}]}' through B
    And token "h" sends "POST /api/v1/contexts" with '{"name":"Closed","space":"{team}","configAttachments":[{"id":"A","type":"ENVIRONMENT_VARIABLE","value":"1","writeOnly":false}],"stackAttachments":[{"stack":"infra","priority":0}]}' through A
    And token "h" sends "PUT /api/v1/stacks/web/contexts/open" with '{"priority":0}' through B
    And the answer's id names "att"
    And the member key is space-reader on the space "legacy"
    When token "m" sends "GET /api/v1/contexts" through A
    Then the answer lists the contexts "open"
    When token "m" sends "POST /api/v1/contexts/search" with '{}' through B
    Then the answer lists the contexts "open"
    When token "m" sends "POST /api/v1/contexts/search/suggestions" with '{"fields":["label"]}' through A
    Then it answers 200
    When token "m" sends "GET /api/v1/contexts/open" through B
    Then it answers 200
    When token "m" sends "GET /api/v1/contexts/open/config-element?id=A" through A
    Then the answer's value is '"1"'
    When token "m" sends "GET /api/v1/contexts/open/attached-stack?id={att}" through B
    Then it answers 200
    When token "m" sends "GET /api/v1/contexts/closed" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/contexts/closed/config-element?id=A" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/attachable-contexts-in-space?id={team}" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/space-contexts/search" with '{"space":"{team}","input":{}}' through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/space-contexts/search/suggestions" with '{"space":"{team}","input":{"fields":["label"]}}' through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/contexts" with '{"name":"Mine","space":"legacy"}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"CONTEXT_CREATE"'
    When token "m" sends "PUT /api/v1/contexts/open" with '{"input":{"name":"Open","space":"legacy"}}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"CONTEXT_UPDATE"'
    When token "m" sends "PATCH /api/v1/contexts/open" with '{"input":{"description":"mine"}}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"CONTEXT_UPDATE"'
    When token "m" sends "POST /api/v1/contexts/open/configs" with '{"id":"B","type":"ENVIRONMENT_VARIABLE","value":"2","writeOnly":false}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"CONTEXT_UPDATE"'
    When token "m" sends "PUT /api/v1/contexts/open/configs/B" with '{"type":"ENVIRONMENT_VARIABLE","value":"2","writeOnly":false}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"CONTEXT_UPDATE"'
    When token "m" sends "DELETE /api/v1/contexts/open/configs/A" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"CONTEXT_UPDATE"'
    When token "m" sends "PUT /api/v1/stacks/web/contexts/open" with '{"priority":1}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_CONTEXT_ATTACH"'
    When token "m" sends "DELETE /api/v1/context-attachments/{att}" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_CONTEXT_DETACH"'
    When token "m" sends "DELETE /api/v1/contexts/open" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"CONTEXT_DELETE"'

  @op:contexts @op:context @op:contextCreateV2 @op:contextUpdateV2 @op:contextPatch @op:contextDelete @op:contextConfigAdd @op:contextConfigSet @op:contextConfigDelete @op:contextConfigElement @op:contextAttach @op:contextDetach @op:contextAttachedStack @op:searchContexts @op:attachableContextsInSpace @op:searchSpaceContexts @op:searchSpaceContextsSuggestions @case:failure
  Scenario: A context needs a name, a free slug, sound elements and stacks in its reach
    When token "h" sends "POST /api/v1/contexts" with '{"name":"","space":"{team}"}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "POST /api/v1/contexts" with '{"name":"Shared","space":"nowhere"}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/contexts" with '{"name":"Shared","space":"{team}","configAttachments":[{"id":"/etc/hosts","type":"FILE_MOUNT","value":"","writeOnly":false}]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/configAttachments/0/id" as "rule"
    When token "h" sends "POST /api/v1/contexts" with '{"name":"Shared","space":"{team}"}' through B
    Then it answers 201
    When token "h" sends "POST /api/v1/contexts" with '{"name":"shared","space":"legacy"}' through A
    Then it answers a 409 problem "already-exists"
    When token "h" sends "POST /api/v1/contexts" with '{"name":"Far","space":"legacy","stackAttachments":[{"stack":"infra","priority":0}]}' through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "POST /api/v1/contexts" with '{"name":"Far","space":"legacy","stackAttachments":[{"stack":"gone","priority":0}]}' through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "PUT /api/v1/contexts/shared" with '{"input":{"name":"Shared","space":"{team}","stackAttachments":[{"stack":"infra","priority":0}]}}' through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "PATCH /api/v1/contexts/shared" with '{"input":{"name":""}}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "GET /api/v1/contexts?cursor=nope" through B
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "POST /api/v1/contexts/search" with '{"after":"nope"}' through A
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "GET /api/v1/attachable-contexts-in-space?id=nowhere" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/space-contexts/search" with '{"space":"nowhere","input":{}}' through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/space-contexts/search/suggestions" with '{"space":"nowhere","input":{"fields":["label"]}}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/contexts/gone" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "PATCH /api/v1/contexts/gone" with '{"input":{"description":"x"}}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "DELETE /api/v1/contexts/gone" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/contexts/shared/config-element?id=NOPE" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/contexts/shared/configs" with '{"id":"A=B","type":"ENVIRONMENT_VARIABLE","value":"1","writeOnly":false}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/id" as "rule"
    When token "h" sends "PUT /api/v1/contexts/shared/configs/NEW" with '{"type":"ENVIRONMENT_VARIABLE","writeOnly":false}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/value" as "required"
    When token "h" sends "DELETE /api/v1/contexts/shared/configs/NOPE" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "PUT /api/v1/stacks/gone/contexts/shared" with '{"priority":0}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "PUT /api/v1/stacks/infra/contexts/gone" with '{"priority":0}' through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/contexts/shared/attached-stack?id=nope" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "DELETE /api/v1/context-attachments/01JAAAAAAAAAAAAAAAAAAAAAAA" through A
    Then it answers a 404 problem "not-found"

  @op:contextAttach @op:contextUpdateV2 @op:contextDelete @op:stackUpdate @op:spaceUpdate @op:stackDelete @case:failure @case:success
  Scenario: A context and its stacks stay in each other's reach, and an attached context is deleted only when forced
    Given token "h" sends "POST /api/v1/contexts" with '{"name":"Shared","space":"{team}","stackAttachments":[{"stack":"infra","priority":0}]}' through A
    And token "h" sends "POST /api/v1/contexts" with '{"name":"Far","space":"legacy"}' through B
    When token "h" sends "PUT /api/v1/stacks/infra/contexts/far" with '{"priority":0}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"main","space":"legacy"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "context shared"
    When token "h" sends "PUT /api/v1/spaces/{child}" with '{"name":"Child","description":"","inheritEntities":true,"parentSpace":"legacy"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "context shared"
    When token "h" sends "PUT /api/v1/contexts/shared" with '{"input":{"name":"Shared","space":"legacy"}}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "infra"
    When token "h" sends "PUT /api/v1/contexts/shared" with '{"input":{"name":"Shared","space":"{child}"}}' through A
    Then it answers 200
    And the answer's space is '"{child}"'
    When token "h" sends "DELETE /api/v1/contexts/shared" through B
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "attached"
    When token "h" sends "DELETE /api/v1/stacks/infra" through A
    Then it answers 200
    When token "h" sends "DELETE /api/v1/contexts/shared" through B
    Then it answers 200

  @op:context @op:contextPatch @op:contextUpdateV2 @op:contextDelete @case:failure @case:success
  Scenario: On a context, an If-Match of a version since changed answers 412
    Given token "h" sends "POST /api/v1/contexts" with '{"name":"Shared","space":"{team}"}' through A
    And token "h" sends "GET /api/v1/contexts/shared" through B
    And the answer's ETag is kept as "first"
    When token "h" sends "PATCH /api/v1/contexts/shared" with '{"input":{"description":"ours"}}' if matching the ETag "first" through A
    Then it answers 200
    And the answer's ETag is kept as "second"
    When token "h" sends "PUT /api/v1/contexts/shared" with '{"input":{"name":"Shared","space":"{team}"}}' if matching the ETag "first" through B
    Then it answers a 412 problem "precondition-failed"
    When token "h" sends "PATCH /api/v1/contexts/shared" with '{"input":{"description":""}}' if matching the ETag "first" through A
    Then it answers a 412 problem "precondition-failed"
    When token "h" sends "DELETE /api/v1/contexts/shared" if matching the ETag "first" through B
    Then it answers a 412 problem "precondition-failed"
    When token "h" sends "GET /api/v1/contexts/shared" through A
    Then the answer's description is '"ours"'
    When token "h" sends "DELETE /api/v1/contexts/shared" if matching the ETag "second" through B
    Then it answers 200
