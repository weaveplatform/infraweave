@postgres
Feature: AWS integrations
  An AWS integration lives in a space and names an IAM role that runs of
  the stacks it is attached to assume, for their read phases, their write
  phases or both. A stack attaches the integrations of its own space and
  of the spaces it reaches while each inherits, and holds one read and one
  write attachment. Before an attachment is stored, the server assumes the
  role with the external ID of each side it serves, so a role whose trust
  policy does not name the installation's AWS account and that external
  ID is refused. A stack's legacy AWS role is an integration bound to the
  stack, attached for both sides, which lists only on request. Account
  admins create, change, delete and attach integrations; a space reader
  reads those of its spaces, and one in a space the caller cannot read
  answers as one that does not exist. The servers hold AWS credentials of
  account 111111111111, which a fake AWS STS knows; team inherits from
  acme, and stack infra lives in team.

  Background:
    Given the servers hold AWS credentials of the account "111111111111"
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"provider":"GIT","name":"Infra","repository":"infra","branch":"main","space":"{team}"},"manageState":false}' through A

  @op:awsIntegrationCreate @op:awsIntegration @op:awsIntegrations @op:awsIntegrationByName @op:awsIntegrationUpdate @op:awsIntegrationPatch @op:awsIntegrationDelete @op:awsIntegrationsEnabled @op:installationAwsAccountId @case:success
  Scenario: An account admin creates an AWS integration, reads, changes and deletes it
    When token "h" sends "GET /api/v1/aws-integrations-enabled" through A
    Then the answer is 'true'
    When token "h" sends "GET /api/v1/installation-aws-account-id" through B
    Then the answer is '"111111111111"'
    When token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Prod","space":"{team}","roleArn":"arn:aws:iam::222222222222:role/deploy","region":"eu-west-1","generateCredentialsInWorker":false,"labels":["b","a"]}' through A
    Then it answers 201
    And the answer's id names "prod"
    And the answer's space is '"{team}"'
    And the answer's labels is '["a","b"]'
    And the answer's durationSeconds is '3600'
    And the answer's attached stacks are ""
    When token "h" sends "GET /api/v1/aws-integrations/{prod}" through B
    Then it answers 200
    And the answer's roleArn is '"arn:aws:iam::222222222222:role/deploy"'
    And the answer's ETag is kept as "first"
    When token "h" sends "GET /api/v1/aws-integration-by-name?name=Prod" through A
    Then the answer's id is '"{prod}"'
    When token "h" sends "GET /api/v1/aws-integrations" through B
    Then the answer lists the AWS integrations "Prod"
    When token "h" sends "PUT /api/v1/aws-integrations/{prod}" with '{"name":"Production","space":"{team}","roleArn":"arn:aws:iam::222222222222:role/deploy","generateCredentialsInWorker":true,"durationSeconds":43200,"externalID":"ours","labels":[]}' through A
    Then it answers 200
    And the answer's name is '"Production"'
    And the answer's externalId is '"ours"'
    And the answer's ETag differs from "first"
    When token "h" sends "PATCH /api/v1/aws-integrations/{prod}" with '{"durationSeconds":900}' through B
    Then it answers 200
    And the answer's durationSeconds is '900'
    And the answer's name is '"Production"'
    When token "h" sends "DELETE /api/v1/aws-integrations/{prod}" through A
    Then it answers 200
    And the answer's name is '"Production"'
    When token "h" sends "GET /api/v1/aws-integrations/{prod}" through B
    Then it answers a 404 problem "not-found"

  @op:awsIntegrationAttach @op:awsIntegrationAttachmentUpdate @op:awsIntegrationAttachedStack @op:awsIntegrationDetach @op:awsIntegrationExternalIdForStack @op:attachableAwsIntegrationsInSpace @op:stackAttachableAwsIntegrations @case:success
  Scenario: An integration attaches to a stack once its role trusts the installation with the stack's external IDs
    Given token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Deploy","space":"{team}","roleArn":"arn:aws:iam::222222222222:role/deploy","generateCredentialsInWorker":false,"labels":[]}' through A
    And the answer's id names "deploy"
    And the AWS role "arn:aws:iam::222222222222:role/deploy" trusts the installation with the external ID "acme@{deploy}@infra@*"
    When token "h" sends "GET /api/v1/aws-integrations/{deploy}/external-id-for-stack?stackId=infra&read=true&write=true" through B
    Then it answers 200
    And the answer's externalId is '"acme@{deploy}@infra@write"'
    When token "h" sends "PUT /api/v1/stacks/infra/aws-integrations/{deploy}" with '{"read":true,"write":false}' through A
    Then it answers 200
    And the answer's id names "att"
    And the answer's stackId is '"infra"'
    And AWS STS was asked to assume the role "arn:aws:iam::222222222222:role/deploy" with the external ID "acme@{deploy}@infra@read"
    When token "h" sends "PUT /api/v1/aws-integration-attachments/{att}" with '{"read":true,"write":true}' through B
    Then it answers 200
    And the answer's write is 'true'
    And AWS STS was asked to assume the role "arn:aws:iam::222222222222:role/deploy" with the external ID "acme@{deploy}@infra@write"
    When token "h" sends "GET /api/v1/aws-integrations/{deploy}/attached-stack?id={att}" through A
    Then it answers 200
    And the answer's stackName is '"Infra"'
    When token "h" sends "GET /api/v1/aws-integrations/{deploy}" through B
    Then the answer's attached stacks are "infra:rw"
    When token "h" sends "GET /api/v1/stacks/infra/attachable-aws-integrations" through A
    Then the answer lists the AWS integrations "Deploy"
    When token "h" sends "GET /api/v1/attachable-aws-integrations-in-space?id={team}" through B
    Then the answer lists the AWS integrations "Deploy"
    When token "h" sends "GET /api/v1/attachable-aws-integrations-in-space?id=legacy" through A
    Then the answer lists the AWS integrations ""
    When token "h" sends "DELETE /api/v1/aws-integration-attachments/{att}" through B
    Then it answers 200
    When token "h" sends "GET /api/v1/aws-integrations/{deploy}/attached-stack?id={att}" through A
    Then it answers a 404 problem "not-found"

  @op:stackIntegrationAwsCreate @op:stackIntegrationAwsDelete @op:awsIntegrations @op:awsIntegration @case:success
  Scenario: A stack's legacy AWS role and an autoattach label each attach an integration to it
    Given the AWS role "arn:aws:iam::222222222222:role/legacy" trusts the installation with the external ID "acme@infra"
    When token "h" sends "PUT /api/v1/stacks/infra/aws-role" with '{"roleArn":"arn:aws:iam::222222222222:role/legacy"}' through A
    Then it answers 200
    And the answer's activated is 'true'
    And the answer's assumedRoleArn is '"arn:aws:iam::222222222222:role/legacy"'
    And AWS STS was asked to assume the role "arn:aws:iam::222222222222:role/legacy" with the external ID "acme@infra"
    When token "h" sends "GET /api/v1/aws-integrations" through B
    Then the answer lists the AWS integrations ""
    When token "h" sends "GET /api/v1/aws-integrations?includeLegacy=true" through A
    Then each item's legacy is 'true'
    When token "h" sends "DELETE /api/v1/stacks/infra/aws-role" through B
    Then it answers 200
    And the answer's activated is 'false'
    When token "h" sends "GET /api/v1/aws-integrations?includeLegacy=true" through A
    Then the answer lists the AWS integrations ""
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"provider":"GIT","name":"Web","repository":"web","branch":"main","space":"{team}","labels":["prod"]},"manageState":false}' through B
    When token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Reader","space":"acme","roleArn":"arn:aws:iam::222222222222:role/reader","generateCredentialsInWorker":false,"labels":["autoattach_read:prod"],"autoattachEnabled":true}' through A
    Then it answers 201
    And the answer's attached stacks are "web:r:auto"

  @op:awsIntegrations @op:awsIntegration @op:awsIntegrationByName @op:awsIntegrationCreate @op:awsIntegrationUpdate @op:awsIntegrationPatch @op:awsIntegrationDelete @op:awsIntegrationsEnabled @op:installationAwsAccountId @op:awsIntegrationAttachedStack @op:awsIntegrationExternalIdForStack @op:awsIntegrationAttach @op:awsIntegrationAttachmentUpdate @op:awsIntegrationDetach @op:stackIntegrationAwsCreate @op:stackIntegrationAwsDelete @op:attachableAwsIntegrationsInSpace @op:stackAttachableAwsIntegrations @case:denial
  Scenario: A space reader reads the AWS integrations of its spaces alone and changes none
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"provider":"GIT","name":"Web","repository":"web","branch":"main","space":"legacy"},"manageState":false}' through A
    And token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Open","space":"legacy","roleArn":"arn:aws:iam::222222222222:role/open","generateCredentialsInWorker":true,"labels":[]}' through B
    And the answer's id names "open"
    And token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Closed","space":"{team}","roleArn":"arn:aws:iam::222222222222:role/closed","generateCredentialsInWorker":true,"labels":[]}' through A
    And the answer's id names "closed"
    And token "h" sends "PUT /api/v1/stacks/web/aws-integrations/{open}" with '{"read":true,"write":true}' through B
    And the answer's id names "att"
    And the member key is space-reader on the space "legacy"
    When token "m" sends "GET /api/v1/aws-integrations" through A
    Then the answer lists the AWS integrations "Open"
    When token "m" sends "GET /api/v1/aws-integrations/{open}" through B
    Then it answers 200
    When token "m" sends "GET /api/v1/aws-integrations/{open}/attached-stack?id={att}" through A
    Then it answers 200
    When token "m" sends "GET /api/v1/aws-integrations-enabled" through B
    Then the answer is 'true'
    When token "m" sends "GET /api/v1/aws-integrations/{closed}" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/aws-integration-by-name?name=Closed" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/aws-integrations/{closed}/attached-stack?id={att}" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/aws-integrations/{open}/external-id-for-stack?stackId=infra&read=true&write=false" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/attachable-aws-integrations-in-space?id={team}" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/stacks/infra/attachable-aws-integrations" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/aws-integrations" with '{"name":"Mine","space":"legacy","roleArn":"arn:aws:iam::222222222222:role/mine","generateCredentialsInWorker":true,"labels":[]}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_ADMIN"'
    When token "m" sends "PUT /api/v1/aws-integrations/{open}" with '{"name":"Open","space":"legacy","roleArn":"arn:aws:iam::222222222222:role/open","generateCredentialsInWorker":true,"durationSeconds":3600,"externalID":"","labels":[]}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_ADMIN"'
    When token "m" sends "PATCH /api/v1/aws-integrations/{open}" with '{"labels":["mine"]}' through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "DELETE /api/v1/aws-integrations/{open}" through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PUT /api/v1/stacks/web/aws-integrations/{open}" with '{"read":true,"write":false}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_AWS_INTEGRATION_ATTACH"'
    When token "m" sends "PUT /api/v1/aws-integration-attachments/{att}" with '{"read":true,"write":false}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_AWS_INTEGRATION_UPDATE"'
    When token "m" sends "DELETE /api/v1/aws-integration-attachments/{att}" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_AWS_INTEGRATION_DETACH"'
    When token "m" sends "PUT /api/v1/stacks/web/aws-role" with '{"roleArn":"arn:aws:iam::222222222222:role/open"}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_AWS_INTEGRATION_ATTACH"'
    When token "m" sends "DELETE /api/v1/stacks/web/aws-role" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_AWS_INTEGRATION_DETACH"'
    When a caller with no token sends "GET /api/v1/installation-aws-account-id" through B
    Then it answers a 401 problem "unauthenticated"

  @op:awsIntegrations @op:awsIntegration @op:awsIntegrationByName @op:awsIntegrationCreate @op:awsIntegrationUpdate @op:awsIntegrationPatch @op:awsIntegrationDelete @op:awsIntegrationAttachedStack @op:awsIntegrationExternalIdForStack @op:awsIntegrationAttach @op:awsIntegrationAttachmentUpdate @op:awsIntegrationDetach @op:stackIntegrationAwsCreate @op:stackIntegrationAwsDelete @op:attachableAwsIntegrationsInSpace @op:stackAttachableAwsIntegrations @case:failure
  Scenario: An integration needs a sound role, a free name and a trust policy, and a stack holds one read and one write attachment
    When token "h" sends "POST /api/v1/aws-integrations" with '{"name":"","space":"{team}","roleArn":"arn:aws:iam::222222222222:user/deploy","generateCredentialsInWorker":false,"labels":[]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/name" as "required"
    And the problem refuses the field "/roleArn" as "format"
    When token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Long","roleArn":"arn:aws:iam::222222222222:role/x","durationSeconds":43200,"generateCredentialsInWorker":false,"labels":[]}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/durationSeconds" as "rule"
    When token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Lost","space":"nowhere","roleArn":"arn:aws:iam::222222222222:role/x","generateCredentialsInWorker":false,"labels":[]}' through A
    Then it answers a 404 problem "not-found"
    Given token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Deploy","space":"{team}","roleArn":"arn:aws:iam::222222222222:role/deploy","generateCredentialsInWorker":false,"labels":[]}' through B
    And the answer's id names "deploy"
    And token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Other","space":"{team}","roleArn":"arn:aws:iam::222222222222:role/other","generateCredentialsInWorker":true,"labels":[]}' through A
    And the answer's id names "other"
    When token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Deploy","space":"legacy","roleArn":"arn:aws:iam::222222222222:role/x","generateCredentialsInWorker":false,"labels":[]}' through B
    Then it answers a 409 problem "already-exists"
    When token "h" sends "GET /api/v1/aws-integrations?cursor=!!" through A
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "GET /api/v1/aws-integrations/01ARZ3NDEKTSV4RRFFQ69G5FAV" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/aws-integration-by-name?name=Nope" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "PUT /api/v1/aws-integrations/{deploy}" with '{"name":"Other","space":"{team}","roleArn":"arn:aws:iam::222222222222:role/deploy","generateCredentialsInWorker":false,"durationSeconds":3600,"externalID":"","labels":[]}' through B
    Then it answers a 409 problem "already-exists"
    When token "h" sends "PATCH /api/v1/aws-integrations/{deploy}" with '{"externalID":"mine"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/externalID" as "rule"
    When token "h" sends "DELETE /api/v1/aws-integrations/01ARZ3NDEKTSV4RRFFQ69G5FAV" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "PUT /api/v1/stacks/infra/aws-integrations/{deploy}" with '{"read":true,"write":true}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "trust relationship"
    And the problem refuses the field "/read" as "rule"
    And the problem refuses the field "/write" as "rule"
    When token "h" sends "PUT /api/v1/stacks/infra/aws-integrations/{deploy}" with '{"read":false,"write":false}' through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "PUT /api/v1/stacks/gone/aws-integrations/{deploy}" with '{"read":true,"write":false}' through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "PUT /api/v1/stacks/infra/aws-integrations/{other}" with '{"read":true,"write":false}' through B
    Then it answers 200
    And the answer's id names "att"
    When token "h" sends "PUT /api/v1/stacks/infra/aws-integrations/{deploy}" with '{"read":true,"write":false}' through A
    Then it answers a 409 problem "invalid-state"
    Given AWS STS fails every call to assume the role "arn:aws:iam::222222222222:role/deploy"
    When token "h" sends "PUT /api/v1/stacks/infra/aws-integrations/{deploy}" with '{"read":false,"write":true}' through B
    Then it answers a 502 problem "upstream-error"
    When token "h" sends "PUT /api/v1/aws-integration-attachments/01ARZ3NDEKTSV4RRFFQ69G5FAV" with '{"read":true,"write":false}' through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "PUT /api/v1/aws-integration-attachments/{att}" with '{"read":false,"write":false}' through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "DELETE /api/v1/aws-integration-attachments/01ARZ3NDEKTSV4RRFFQ69G5FAV" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/aws-integrations/{deploy}/attached-stack?id=nope" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/aws-integrations/{deploy}/external-id-for-stack?stackId=gone&read=true&write=false" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/attachable-aws-integrations-in-space?id=nowhere" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/stacks/gone/attachable-aws-integrations" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "PUT /api/v1/stacks/infra/aws-role" with '{"roleArn":"arn:aws:iam::222222222222:role/unknown"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/roleArn" as "rule"
    When token "h" sends "DELETE /api/v1/stacks/gone/aws-role" through A
    Then it answers a 404 problem "not-found"
