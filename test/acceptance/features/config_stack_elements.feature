@postgres
Feature: A stack's own config elements
  A stack holds its own config elements, environment variables and
  mounted files, beside those of the contexts it attaches. An element is
  added by its ID, or created and replaced at its own path, and a value
  that is left out keeps the one stored. A write-only value is never
  answered again, by the element, the stack or the contexts the stack may
  attach, only its checksum. Deleting a stack deletes its elements. A
  space writer adds and deletes a stack's elements; a space reader reads
  them. A value's ${{ … }} templates are checked when it is saved, a
  stack's against the variables the stack resolves to, and kept
  unrendered; one that reads a write-only value is write-only, and
  ${NAME} is left to the shell. A stack in a space the caller cannot read answers as one that does
  not exist. The home key is space-admin on the account's top space; the
  member key holds only the bindings a scenario gives it. Team inherits
  from acme; stack infra lives in legacy and stack hidden in team.

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through B
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Hidden","repository":"hidden","branch":"main","space":"{team}"},"manageState":false}' through A

  @op:stackConfigAdd @op:stackConfigSet @op:stackConfigElement @op:stackConfigDelete @op:stack @op:stackDelete @case:success
  Scenario: A stack's elements are added, replaced, read and deleted, and go with the stack
    When token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TOKEN","type":"ENVIRONMENT_VARIABLE","value":"s3cret","writeOnly":true}' through A
    Then it answers 201
    And the answer's checksum is '"1ec1c26b50d5d3c58d9583181af8076655fe00756bf7285940ba3670f99fcba0"'
    When token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TOKEN","type":"ENVIRONMENT_VARIABLE","value":"s3cret","writeOnly":true,"description":"the token"}' through B
    Then it answers 200
    And the answer's description is '"the token"'
    When token "h" sends "PUT /api/v1/stacks/infra/configs/app.toml" with '{"type":"FILE_MOUNT","value":"a2V5ID0gMQo=","writeOnly":false,"fileMode":"600"}' through A
    Then it answers 201
    When token "h" sends "PUT /api/v1/stacks/infra/configs/app.toml" with '{"type":"FILE_MOUNT","writeOnly":false,"description":"settings"}' through B
    Then it answers 200
    And the answer's value is '"a2V5ID0gMQo="'
    And the answer's description is '"settings"'
    When token "h" sends "GET /api/v1/stacks/infra/config-element?id=TOKEN" through A
    Then it answers 200
    And the answer's checksum is '"1ec1c26b50d5d3c58d9583181af8076655fe00756bf7285940ba3670f99fcba0"'
    And the answer leaves out "s3cret"
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then the answer's config lists the elements "TOKEN, app.toml"
    And the answer leaves out "s3cret"
    When token "h" sends "DELETE /api/v1/stacks/infra/configs/TOKEN" through A
    Then it answers 200
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then the answer's config lists the elements "app.toml"
    When token "h" sends "DELETE /api/v1/stacks/infra" through A
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through B
    Then it answers 201
    And the answer's config lists the elements ""

  @op:stackAttachableContexts @op:stack @case:success
  Scenario: The contexts a stack may attach answer no write-only value
    Given token "h" sends "POST /api/v1/contexts" with '{"name":"Top","space":"acme","configAttachments":[{"id":"TOKEN","type":"ENVIRONMENT_VARIABLE","value":"s3cret","writeOnly":true}]}' through A
    And token "h" sends "POST /api/v1/contexts" with '{"name":"Ours","space":"{team}"}' through B
    When token "h" sends "GET /api/v1/stacks/hidden/attachable-contexts" through A
    Then the answer lists the contexts "ours, top"
    And the answer leaves out "s3cret"
    When token "h" sends "PUT /api/v1/stacks/hidden/contexts/top" with '{"priority":0}' through B
    And token "h" sends "GET /api/v1/stacks/hidden" through A
    Then it answers 200
    And the answer leaves out "s3cret"

  @op:stackConfigAdd @op:stackConfigSet @op:stackConfigElement @op:stackConfigDelete @op:stackAttachableContexts @case:denial
  Scenario: A space reader reads a stack's elements and changes none; a writer changes them
    Given token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"REGION","type":"ENVIRONMENT_VARIABLE","value":"eu","writeOnly":false}' through A
    And token "h" sends "POST /api/v1/stacks/hidden/configs" with '{"id":"REGION","type":"ENVIRONMENT_VARIABLE","value":"eu","writeOnly":false}' through B
    And the member key is space-reader on the space "legacy"
    When token "m" sends "GET /api/v1/stacks/infra/config-element?id=REGION" through A
    Then the answer's value is '"eu"'
    When token "m" sends "GET /api/v1/stacks/infra/attachable-contexts" through B
    Then it answers 200
    When token "m" sends "POST /api/v1/stacks/infra/configs" with '{"id":"MINE","type":"ENVIRONMENT_VARIABLE","value":"1","writeOnly":false}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_ADD_CONFIG"'
    When token "m" sends "PUT /api/v1/stacks/infra/configs/MINE" with '{"type":"ENVIRONMENT_VARIABLE","value":"1","writeOnly":false}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_ADD_CONFIG"'
    When token "m" sends "DELETE /api/v1/stacks/infra/configs/REGION" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_DELETE_CONFIG"'
    When token "m" sends "GET /api/v1/stacks/hidden/config-element?id=REGION" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/stacks/hidden/attachable-contexts" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/stacks/hidden/configs" with '{"id":"MINE","type":"ENVIRONMENT_VARIABLE","value":"1","writeOnly":false}' through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "PUT /api/v1/stacks/hidden/configs/MINE" with '{"type":"ENVIRONMENT_VARIABLE","value":"1","writeOnly":false}' through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "DELETE /api/v1/stacks/hidden/configs/REGION" through B
    Then it answers a 404 problem "not-found"
    Given the member key is space-writer on the space "legacy"
    When token "m" sends "PUT /api/v1/stacks/infra/configs/MINE" with '{"type":"ENVIRONMENT_VARIABLE","value":"1","writeOnly":false}' through A
    Then it answers 201
    When token "m" sends "DELETE /api/v1/stacks/infra/configs/MINE" through B
    Then it answers 200

  @op:stackConfigAdd @op:stackConfigSet @op:stackConfigElement @op:stackConfigDelete @op:stackAttachableContexts @case:failure
  Scenario: An element needs a sound name and a value, and a stack and element the account holds
    Given token "h" sends "PUT /api/v1/stacks/infra/configs/TOKEN" with '{"type":"ENVIRONMENT_VARIABLE","value":"s3cret","writeOnly":true}' through A
    When token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"A=B","type":"ENVIRONMENT_VARIABLE","value":"1","writeOnly":false}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/id" as "rule"
    When token "h" sends "PUT /api/v1/stacks/infra/configs/NEW" with '{"type":"ENVIRONMENT_VARIABLE","writeOnly":false}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/value" as "required"
    When token "h" sends "PUT /api/v1/stacks/infra/configs/TOKEN" with '{"type":"ENVIRONMENT_VARIABLE","writeOnly":false}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/value" as "required"
    When token "h" sends "PUT /api/v1/stacks/infra/configs/A=B" with '{"type":"ENVIRONMENT_VARIABLE","value":"1","writeOnly":false}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/configId" as "rule"
    When token "h" sends "GET /api/v1/stacks/infra/config-element?id=NOPE" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "DELETE /api/v1/stacks/infra/configs/NOPE" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/stacks/infra/attachable-contexts?cursor=nope" through B
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "GET /api/v1/stacks/gone/config-element?id=TOKEN" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/stacks/gone/attachable-contexts" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks/gone/configs" with '{"id":"A","type":"ENVIRONMENT_VARIABLE","value":"1","writeOnly":false}' through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "PUT /api/v1/stacks/gone/configs/A" with '{"type":"ENVIRONMENT_VARIABLE","value":"1","writeOnly":false}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "DELETE /api/v1/stacks/gone/configs/A" through A
    Then it answers a 404 problem "not-found"

  @op:stackConfigAdd @op:stackConfigSet @op:stackConfigElement @op:contextConfigAdd @case:failure @case:success
  Scenario: A value's templates are checked when it is saved and kept unrendered
    Given token "h" sends "PUT /api/v1/stacks/infra/configs/REGION" with '{"type":"ENVIRONMENT_VARIABLE","value":"eu","writeOnly":false}' through A
    And token "h" sends "PUT /api/v1/stacks/infra/configs/TOKEN" with '{"type":"ENVIRONMENT_VARIABLE","value":"s3cret","writeOnly":true}' through B
    When token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"URL","type":"ENVIRONMENT_VARIABLE","value":"https://${{ env.TYPO }}.example","writeOnly":false}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/value" from 1:17 to 1:21
    When token "h" sends "PUT /api/v1/stacks/infra/configs/URL" with '{"type":"ENVIRONMENT_VARIABLE","value":"${{ stack.nmae }}","writeOnly":false}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/value" from 1:11 to 1:15
    When token "h" sends "PUT /api/v1/stacks/infra/configs/URL" with '{"type":"ENVIRONMENT_VARIABLE","value":"${{ run.nope }}","writeOnly":false}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/value" from 1:9 to 1:13
    When token "h" sends "PUT /api/v1/stacks/infra/configs/COMMIT" with '{"type":"ENVIRONMENT_VARIABLE","value":"${{ run.commit.hash }}","writeOnly":false}' through B
    Then it answers 201
    When token "h" sends "PUT /api/v1/stacks/infra/configs/URL" with '{"type":"ENVIRONMENT_VARIABLE","value":"https://${{ env.REGION }}.${{ stack.id }}/${HOME}","writeOnly":false}' through A
    Then it answers 201
    When token "h" sends "GET /api/v1/stacks/infra/config-element?id=URL" through B
    Then the answer's value is '"https://${{ env.REGION }}.${{ stack.id }}/${HOME}"'
    When token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"AUTH","type":"ENVIRONMENT_VARIABLE","value":"Bearer ${{ env.TOKEN }}","writeOnly":false}' through A
    Then it answers 201
    And the answer's writeOnly is 'true'
    When token "h" sends "GET /api/v1/stacks/infra/config-element?id=AUTH" through B
    Then the answer leaves out "Bearer"
    Given token "h" sends "POST /api/v1/contexts" with '{"name":"Shared","space":"legacy"}' through B
    When token "h" sends "POST /api/v1/contexts/shared/configs" with '{"id":"URL","type":"ENVIRONMENT_VARIABLE","value":"${{ env.FROM_A_STACK }}","writeOnly":false}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/contexts/shared/configs" with '{"id":"BAD","type":"ENVIRONMENT_VARIABLE","value":"${{ env.FROM_A_STACK","writeOnly":false}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/value" from 1:1 to 1:4
