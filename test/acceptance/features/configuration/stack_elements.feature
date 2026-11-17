@postgres
Feature: A stack's own config elements and resolved environment
  A stack holds its own config elements, environment variables and
  mounted files, beside those of the contexts it attaches. An element is
  added by its ID, or created and replaced at its own path, and a value
  that is left out keeps the one stored. A new element cannot take a name
  an attached context sets; a name the stack already owns can be replaced.
  A write-only value is never
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
  Its runtimeConfig lists the winning stored sources with their context
  attachments and the computed run metadata without a value. Its config
  keeps only its own elements. Direct, list and search reads share this
  resolved view.

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

  @op:stackConfigAdd @op:stackConfigSet @op:contextAttach @case:success
  Scenario: Stack names survive context attachment, context writes and labels, and detach frees a name
    Given token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"LEVEL","type":"ENVIRONMENT_VARIABLE","value":"stack","writeOnly":false}' through A
    Then it answers 201
    And token "h" sends "POST /api/v1/contexts" with '{"name":"Shared","space":"legacy","configAttachments":[{"id":"LEVEL","type":"ENVIRONMENT_VARIABLE","value":"context","writeOnly":false},{"id":"REGION","type":"ENVIRONMENT_VARIABLE","value":"eu","writeOnly":false}]}' through B
    And token "h" sends "PUT /api/v1/stacks/infra/contexts/shared" with '{"priority":0}' through A
    Then it answers 200
    And the answer's id names "att"
    When token "h" sends "PUT /api/v1/stacks/infra/configs/LEVEL" with '{"type":"ENVIRONMENT_VARIABLE","value":"replaced","writeOnly":false}' through B
    Then it answers 200
    And the answer's value is '"replaced"'
    When token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"LEVEL","type":"ENVIRONMENT_VARIABLE","value":"again","writeOnly":false}' through A
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"LATE","type":"ENVIRONMENT_VARIABLE","value":"stack","writeOnly":false}' through B
    Then it answers 201
    When token "h" sends "POST /api/v1/contexts/shared/configs" with '{"id":"LATE","type":"ENVIRONMENT_VARIABLE","value":"context","writeOnly":false}' through A
    Then it answers 201
    Given token "h" sends "POST /api/v1/contexts" with '{"name":"Labelled","space":"legacy","labels":["autoattach:infra"],"configAttachments":[{"id":"LEVEL","type":"ENVIRONMENT_VARIABLE","value":"automatic","writeOnly":false}]}' through B
    When token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"main","space":"legacy","labels":["infra"]}' through A
    Then it answers 200
    And the stack "infra" read through B with token "h" eventually attaches the contexts "shared, auto:labelled"
    When token "h" sends "DELETE /api/v1/context-attachments/{att}" through A
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"REGION","type":"ENVIRONMENT_VARIABLE","value":"free","writeOnly":false}' through B
    Then it answers 201
    And the answer's value is '"free"'

  @op:stackConfigAdd @op:stackConfigSet @case:failure
  Scenario: A new stack name cannot hide a direct or auto-attached context element
    Given token "h" sends "POST /api/v1/contexts" with '{"name":"Shared","space":"legacy","configAttachments":[{"id":"REGION","type":"ENVIRONMENT_VARIABLE","value":"eu","writeOnly":false},{"id":"app.toml","type":"FILE_MOUNT","value":"a2V5","writeOnly":true}]}' through A
    And token "h" sends "PUT /api/v1/stacks/infra/contexts/shared" with '{"priority":0}' through B
    When token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"REGION","type":"ENVIRONMENT_VARIABLE","value":"us","writeOnly":false}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/id" as "duplicate"
    When token "h" sends "PUT /api/v1/stacks/infra/configs/REGION" with '{"type":"ENVIRONMENT_VARIABLE","value":"us","writeOnly":false}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/configId" as "duplicate"
    When token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"REGION","type":"FILE_MOUNT","value":"dXM=","writeOnly":false}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/id" as "duplicate"
    When token "h" sends "PUT /api/v1/stacks/infra/configs/app.toml" with '{"type":"ENVIRONMENT_VARIABLE","value":"plain","writeOnly":false}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/configId" as "duplicate"
    Given token "h" sends "POST /api/v1/contexts" with '{"name":"Automatic","space":"legacy","labels":["autoattach:infra"],"configAttachments":[{"id":"ZONE","type":"ENVIRONMENT_VARIABLE","value":"a","writeOnly":false}]}' through A
    And token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"main","space":"legacy","labels":["infra"]}' through B
    And the stack "infra" read through A with token "h" eventually attaches the contexts "shared, auto:automatic"
    When token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"ZONE","type":"ENVIRONMENT_VARIABLE","value":"b","writeOnly":false}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/id" as "duplicate"
    When token "h" sends "PUT /api/v1/stacks/infra/configs/ZONE" with '{"type":"ENVIRONMENT_VARIABLE","value":"b","writeOnly":false}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/configId" as "duplicate"
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then the answer's config lists the elements ""

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
    And token "h" sends "POST /api/v1/contexts" with '{"name":"Shared","space":"legacy","configAttachments":[{"id":"CLASH","type":"ENVIRONMENT_VARIABLE","value":"context","writeOnly":false}]}' through A
    And token "h" sends "PUT /api/v1/stacks/infra/contexts/shared" with '{"priority":0}' through B
    And the member key is space-reader on the space "legacy"
    When token "m" sends "GET /api/v1/stacks/infra/config-element?id=REGION" through A
    Then the answer's value is '"eu"'
    When token "m" sends "GET /api/v1/stacks/infra/attachable-contexts" through B
    Then it answers 200
    When token "m" sends "POST /api/v1/stacks/infra/configs" with '{"id":"CLASH","type":"ENVIRONMENT_VARIABLE","value":"stack","writeOnly":false}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_ADD_CONFIG"'
    When token "m" sends "PUT /api/v1/stacks/infra/configs/CLASH" with '{"type":"ENVIRONMENT_VARIABLE","value":"stack","writeOnly":false}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_ADD_CONFIG"'
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

  @op:stack @op:stacks @op:searchStacks @case:success
  Scenario: Direct, list and search reads share the winning sources and computed run metadata
    Given token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"LEVEL","type":"ENVIRONMENT_VARIABLE","value":"stack","writeOnly":false}' through A
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TOKEN","type":"ENVIRONMENT_VARIABLE","value":"runtime-view-s3cret","writeOnly":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"a.txt","type":"FILE_MOUNT","value":"YQ==","writeOnly":false,"fileMode":"600","description":"mounted"}' through A
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_infraweave_run_type","type":"ENVIRONMENT_VARIABLE","value":"stack","writeOnly":false}' through B
    And token "h" sends "POST /api/v1/contexts" with '{"name":"Shared","space":"legacy","configAttachments":[{"id":"CTX_ONLY","type":"ENVIRONMENT_VARIABLE","value":"context","writeOnly":false},{"id":"LEVEL","type":"ENVIRONMENT_VARIABLE","value":"context","writeOnly":false}]}' through A
    And token "h" sends "PUT /api/v1/stacks/infra/contexts/shared" with '{"priority":1}' through B
    And token "h" sends "POST /api/v1/contexts" with '{"name":"Automatic","space":"legacy","labels":["autoattach:infra"],"configAttachments":[{"id":"AUTO","type":"ENVIRONMENT_VARIABLE","value":"automatic","writeOnly":false}]}' through A
    And token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"main","space":"legacy","labels":["infra"]}' through B
    And the stack "infra" read through A with token "h" eventually attaches the contexts "shared, auto:automatic"
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then it answers 200
    And the stack "infra" in the answer lists runtime elements "AUTO, CTX_ONLY, INFRAWEAVE_API_TOKEN, LEVEL, TF_VAR_infraweave_account_name, TF_VAR_infraweave_api_endpoint, TF_VAR_infraweave_commit_branch, TF_VAR_infraweave_commit_sha, TF_VAR_infraweave_local_preview, TF_VAR_infraweave_project_root, TF_VAR_infraweave_repository, TF_VAR_infraweave_run_id, TF_VAR_infraweave_run_state, TF_VAR_infraweave_run_trigger, TF_VAR_infraweave_run_type, TF_VAR_infraweave_space_id, TF_VAR_infraweave_stack_branch, TF_VAR_infraweave_stack_id, TF_VAR_infraweave_stack_labels, TF_VAR_infraweave_stack_labels_list, TF_VAR_infraweave_workspace_root, TOKEN, a.txt"
    And the stack "infra" in the answer has runtime sources:
      | AUTO | {"value":"automatic","runtime":false,"writeOnly":false} | auto:automatic |
      | CTX_ONLY | {"value":"context","runtime":false,"writeOnly":false} | shared |
      | INFRAWEAVE_API_TOKEN | {"value":null,"runtime":true,"writeOnly":true,"category":"INFRAWEAVE_ENVIRONMENT"} | computed |
      | LEVEL | {"value":"stack","runtime":false,"writeOnly":false} | stack |
      | TF_VAR_infraweave_run_type | {"value":"stack","runtime":false,"category":null} | stack |
      | TOKEN | {"value":null,"runtime":false,"writeOnly":true} | stack |
      | a.txt | {"value":"YQ==","fileMode":"600","description":"mounted","runtime":false} | stack |
    And the stack "infra" in the answer lists only stored config elements "LEVEL, TF_VAR_infraweave_run_type, TOKEN, a.txt"
    And the stack "infra" in the answer keeps its runtime configuration as "resolved"
    And the answer leaves out "runtime-view-s3cret"
    When token "h" sends "GET /api/v1/stacks" through B
    Then the stack "infra" in the answer has the runtime configuration "resolved"
    When token "h" sends "POST /api/v1/stacks/search" with '{}' through A
    Then the stack "infra" in the answer has the runtime configuration "resolved"
    When token "h" sends "DELETE /api/v1/stacks/infra/configs/LEVEL" through B
    Then it answers 200
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then the stack "infra" in the answer has runtime sources:
      | LEVEL | {"value":"context","runtime":false,"writeOnly":false} | shared |
    And the stack "infra" in the answer lists only stored config elements "TF_VAR_infraweave_run_type, TOKEN, a.txt"
    And no answer of the scenario, log line of A or B, audit entry or event holds "runtime-view-s3cret"

  @op:stack @op:stacks @op:searchStacks @op:stackConfigAdd @case:denial
  Scenario: A reader sees the same source view without secrets, changes none and sees no hidden stack
    Given token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"LEVEL","type":"ENVIRONMENT_VARIABLE","value":"stack","writeOnly":false}' through A
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TOKEN","type":"ENVIRONMENT_VARIABLE","value":"runtime-view-s3cret","writeOnly":true}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"a.txt","type":"FILE_MOUNT","value":"YQ==","writeOnly":false,"fileMode":"600","description":"mounted"}' through A
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TF_VAR_infraweave_run_type","type":"ENVIRONMENT_VARIABLE","value":"stack","writeOnly":false}' through B
    And token "h" sends "POST /api/v1/contexts" with '{"name":"Shared","space":"legacy","configAttachments":[{"id":"CTX_ONLY","type":"ENVIRONMENT_VARIABLE","value":"context","writeOnly":false},{"id":"LEVEL","type":"ENVIRONMENT_VARIABLE","value":"context","writeOnly":false}]}' through A
    And token "h" sends "PUT /api/v1/stacks/infra/contexts/shared" with '{"priority":1}' through B
    And token "h" sends "POST /api/v1/contexts" with '{"name":"Automatic","space":"legacy","labels":["autoattach:infra"],"configAttachments":[{"id":"AUTO","type":"ENVIRONMENT_VARIABLE","value":"automatic","writeOnly":false}]}' through A
    And token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"main","space":"legacy","labels":["infra"]}' through B
    And the stack "infra" read through A with token "h" eventually attaches the contexts "shared, auto:automatic"
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then the stack "infra" in the answer keeps its runtime configuration as "resolved"
    Given the member key is space-reader on the space "legacy"
    When token "m" sends "GET /api/v1/stacks/infra" through B
    Then the stack "infra" in the answer has the runtime configuration "resolved"
    And the answer leaves out "runtime-view-s3cret"
    When token "m" sends "GET /api/v1/stacks" through A
    Then the stack "infra" in the answer has the runtime configuration "resolved"
    And the answer lists the stacks "infra"
    When token "m" sends "POST /api/v1/stacks/search" with '{}' through B
    Then the stack "infra" in the answer has the runtime configuration "resolved"
    And the answer leaves out "hidden"
    When token "m" sends "POST /api/v1/stacks/infra/configs" with '{"id":"NEW","type":"ENVIRONMENT_VARIABLE","value":"changed","writeOnly":false}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_ADD_CONFIG"'
    When token "m" sends "GET /api/v1/stacks/infra" through B
    Then the stack "infra" in the answer has the runtime configuration "resolved"
    When token "m" sends "GET /api/v1/stacks/hidden" through A
    Then it answers a 404 problem "not-found"
    And no answer of the scenario, log line of A or B, audit entry or event holds "runtime-view-s3cret"

  @op:stack @case:failure
  Scenario: Unknown stacks and stacks held by another account expose no runtime configuration
    Given the sibling key is exchanged through A for token "s"
    And token "s" sends "POST /api/v1/stacks" with '{"input":{"name":"Foreign","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through B
    Then it answers 201
    When token "h" sends "GET /api/v1/stacks/foreign" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/stacks/unknown" through B
    Then it answers a 404 problem "not-found"
