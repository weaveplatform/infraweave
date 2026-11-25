@postgres
Feature: Azure integrations
  An Azure integration lives in a space and names a customer's Entra
  tenant, fixed at creation, and a dedicated multi-tenant Entra application
  the installation creates for it through Microsoft Graph as the operator's
  registered application, which runs of the stacks it is attached to act
  as once the tenant's admin consents. An account admin records that
  consent was granted, or no longer is, and infraweave never checks it
  against Entra. A stack attaches the integrations of its own space and of
  the spaces it reaches while each inherits, with one read and one write
  attachment, each with an optional subscription; auto-attachment needs a
  default subscription. Graph's throttling is retried within the request,
  and a create Graph fails leaves neither an integration nor an
  application. A fake Entra ID and Graph serve the operator's tenant; team
  inherits from acme, and stack infra lives in team.

  Background:
    Given the installation registers an Entra application for Azure integrations
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"provider":"GIT","name":"Infra","repository":"infra","branch":"main","space":"{team}"},"manageState":false}' through A

  @op:azureIntegrationCreate @op:azureIntegration @op:azureIntegrations @op:azureIntegrationByName @op:azureIntegrationUpdate @op:azureIntegrationPatch @op:azureIntegrationDelete @op:azureIntegrationsEnabled @op:azureIntegrationConsentConfirm @op:azureIntegrationConsentClear @op:integrationsCount @case:success
  Scenario: An account admin creates an Azure integration with its Entra application, records consent and deletes both
    When token "h" sends "GET /api/v1/azure-integrations-enabled" through A
    Then the answer is 'true'
    When token "h" sends "POST /api/v1/azure-integrations" with '{"name":"Prod","space":"{team}","tenantID":"9A9B9C9D-0000-1111-2222-333344445555","labels":["b","a"]}' through A
    Then it answers 201
    And the answer's id names "prod"
    And the answer's tenantId is '"9a9b9c9d-0000-1111-2222-333344445555"'
    And the answer's displayName is '"infraweave-acme-{prod}"'
    And the answer's adminConsentURL asks the tenant "9a9b9c9d-0000-1111-2222-333344445555" to consent to its application
    And the answer's adminConsentProvided is 'false'
    And the answer's labels is '["a","b"]'
    And the answer's attached stacks are ""
    And Microsoft Graph holds the Entra application of the Azure integration "prod" of the account "acme"
    And the home account's audit trail holds "azure_integration.create"
    When token "h" sends "GET /api/v1/azure-integrations/{prod}" through B
    Then it answers 200
    And the answer's space is '"{team}"'
    And the answer leaves out "servicePrincipalObjectId"
    And the answer's ETag is kept as "first"
    When token "h" sends "GET /api/v1/azure-integration-by-name?name=Prod" through A
    Then the answer's id is '"{prod}"'
    When token "h" sends "GET /api/v1/azure-integrations" through B
    Then the answer lists the Azure integrations "Prod"
    When token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Worker","roleArn":"arn:aws:iam::222222222222:role/worker","generateCredentialsInWorker":true,"labels":[]}' through A
    And token "h" sends "GET /api/v1/integrations-count" through B
    Then it answers 200
    And the answer's azure is '1'
    And the answer's aws is '1'
    And the answer's github is '0'
    And the answer's slack is '0'
    When token "h" sends "PUT /api/v1/azure-integrations/{prod}" with '{"name":"Production","space":"{team}","defaultSubscriptionId":"11111111-2222-3333-4444-000000000001","labels":[]}' through A
    Then it answers 200
    And the answer's name is '"Production"'
    And the answer's tenantId is '"9a9b9c9d-0000-1111-2222-333344445555"'
    And the answer's defaultSubscriptionId is '"11111111-2222-3333-4444-000000000001"'
    And the answer's ETag differs from "first"
    And the home account's audit trail holds "azure_integration.update"
    When token "h" sends "PATCH /api/v1/azure-integrations/{prod}" with '{"labels":["x"]}' through B
    Then it answers 200
    And the answer's labels is '["x"]'
    And the answer's name is '"Production"'
    When token "h" sends "POST /api/v1/azure-integrations/{prod}/consent-confirm" through A
    Then it answers 200
    And the answer's adminConsentProvided is 'true'
    And the home account's audit trail holds "azure_integration.confirm_consent"
    When token "h" sends "GET /api/v1/azure-integrations/{prod}" through B
    Then the answer's adminConsentProvided is 'true'
    When token "h" sends "POST /api/v1/azure-integrations/{prod}/consent-clear" through B
    Then it answers 200
    And the answer's adminConsentProvided is 'false'
    And the home account's audit trail holds "azure_integration.clear_consent"
    When token "h" sends "DELETE /api/v1/azure-integrations/{prod}" through A
    Then it answers 200
    And the answer's name is '"Production"'
    And Microsoft Graph holds 0 Entra applications
    And the home account's audit trail holds "azure_integration.delete"
    When token "h" sends "GET /api/v1/azure-integrations/{prod}" through B
    Then it answers a 404 problem "not-found"

  @op:azureIntegrationAttach @op:azureIntegrationAttachmentUpdate @op:azureIntegrationAttachedStack @op:azureIntegrationDetach @op:attachableAzureIntegrationsInSpace @op:stackAttachableAzureIntegrations @case:success
  Scenario: An Azure integration attaches to a stack in its reach, with a subscription of its own or the integration's default
    Given token "h" sends "POST /api/v1/azure-integrations" with '{"name":"Deploy","space":"{team}","tenantID":"9a9b9c9d-0000-1111-2222-333344445555","defaultSubscriptionId":"11111111-2222-3333-4444-000000000001","labels":[]}' through A
    And the answer's id names "deploy"
    When token "h" sends "PUT /api/v1/stacks/infra/azure-integrations/{deploy}" with '{"read":true,"write":false,"subscriptionId":"11111111-2222-3333-4444-000000000002"}' through B
    Then it answers 200
    And the answer's id names "att"
    And the answer's stackId is '"infra"'
    And the answer's subscriptionId is '"11111111-2222-3333-4444-000000000002"'
    And the answer's isModule is 'false'
    And the home account's audit trail holds "azure_integration.attach"
    When token "h" sends "PUT /api/v1/azure-integration-attachments/{att}" with '{"read":true,"write":true}' through A
    Then it answers 200
    And the answer's write is 'true'
    And the answer leaves out "subscriptionId"
    And the home account's audit trail holds "azure_integration.update_attachment"
    When token "h" sends "GET /api/v1/azure-integrations/{deploy}/attached-stack?id={att}" through B
    Then it answers 200
    And the answer's stackName is '"Infra"'
    When token "h" sends "GET /api/v1/azure-integrations/{deploy}" through A
    Then the answer's attached stacks are "infra:rw"
    When token "h" sends "GET /api/v1/stacks/infra/attachable-azure-integrations" through B
    Then the answer lists the Azure integrations "Deploy"
    When token "h" sends "GET /api/v1/attachable-azure-integrations-in-space?id={team}" through A
    Then the answer lists the Azure integrations "Deploy"
    When token "h" sends "GET /api/v1/attachable-azure-integrations-in-space?id=legacy" through B
    Then the answer lists the Azure integrations ""
    When token "h" sends "DELETE /api/v1/azure-integration-attachments/{att}" through A
    Then it answers 200
    And the home account's audit trail holds "azure_integration.detach"
    When token "h" sends "GET /api/v1/azure-integrations/{deploy}/attached-stack?id={att}" through B
    Then it answers a 404 problem "not-found"
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"provider":"GIT","name":"Web","repository":"web","branch":"main","space":"{team}","labels":["prod"]},"manageState":false}' through B
    When token "h" sends "POST /api/v1/azure-integrations" with '{"name":"Auto","space":"acme","tenantID":"9a9b9c9d-0000-1111-2222-333344445555","defaultSubscriptionId":"11111111-2222-3333-4444-000000000001","labels":["autoattach:prod"],"autoattachEnabled":true}' through A
    Then it answers 201
    And the answer's attached stacks are "web:rw:auto"

  @op:azureIntegrations @op:azureIntegration @op:azureIntegrationByName @op:azureIntegrationCreate @op:azureIntegrationUpdate @op:azureIntegrationPatch @op:azureIntegrationDelete @op:azureIntegrationsEnabled @op:azureIntegrationAttachedStack @op:azureIntegrationAttach @op:azureIntegrationAttachmentUpdate @op:azureIntegrationDetach @op:azureIntegrationConsentConfirm @op:azureIntegrationConsentClear @op:attachableAzureIntegrationsInSpace @op:stackAttachableAzureIntegrations @op:integrationsCount @case:denial
  Scenario: A space reader reads the Azure integrations of its spaces alone and changes none
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"provider":"GIT","name":"Web","repository":"web","branch":"main","space":"legacy"},"manageState":false}' through A
    And token "h" sends "POST /api/v1/azure-integrations" with '{"name":"Open","space":"legacy","tenantID":"9a9b9c9d-0000-1111-2222-333344445555","labels":[]}' through B
    And the answer's id names "open"
    And token "h" sends "POST /api/v1/azure-integrations" with '{"name":"Closed","space":"{team}","tenantID":"9a9b9c9d-0000-1111-2222-333344445555","labels":[]}' through A
    And the answer's id names "closed"
    And token "h" sends "PUT /api/v1/stacks/web/azure-integrations/{open}" with '{"read":true,"write":true}' through B
    And the answer's id names "att"
    And the member key is space-reader on the space "legacy"
    When token "m" sends "GET /api/v1/azure-integrations" through A
    Then the answer lists the Azure integrations "Open"
    When token "m" sends "GET /api/v1/azure-integrations/{open}" through B
    Then it answers 200
    When token "m" sends "GET /api/v1/azure-integrations/{open}/attached-stack?id={att}" through A
    Then it answers 200
    When token "m" sends "GET /api/v1/azure-integrations-enabled" through B
    Then the answer is 'true'
    When token "m" sends "GET /api/v1/azure-integrations/{closed}" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/azure-integration-by-name?name=Closed" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/azure-integrations/{closed}/attached-stack?id={att}" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/attachable-azure-integrations-in-space?id={team}" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/stacks/infra/attachable-azure-integrations" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/integrations-count" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_READ"'
    When token "m" sends "POST /api/v1/azure-integrations" with '{"name":"Mine","space":"legacy","tenantID":"9a9b9c9d-0000-1111-2222-333344445555","labels":[]}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_ADMIN"'
    When token "m" sends "PUT /api/v1/azure-integrations/{open}" with '{"name":"Open","space":"legacy","labels":[]}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_ADMIN"'
    When token "m" sends "PATCH /api/v1/azure-integrations/{open}" with '{"labels":["mine"]}' through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "DELETE /api/v1/azure-integrations/{open}" through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/azure-integrations/{open}/consent-confirm" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"AZURE_INTEGRATION_CONFIRM_CONSENT"'
    When token "m" sends "POST /api/v1/azure-integrations/{open}/consent-clear" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"AZURE_INTEGRATION_CLEAR_CONSENT"'
    When token "m" sends "PUT /api/v1/stacks/web/azure-integrations/{open}" with '{"read":true,"write":false}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_AZURE_INTEGRATION_ATTACH"'
    When token "m" sends "PUT /api/v1/azure-integration-attachments/{att}" with '{"read":true,"write":false}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_AZURE_INTEGRATION_UPDATE"'
    When token "m" sends "DELETE /api/v1/azure-integration-attachments/{att}" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_AZURE_INTEGRATION_DETACH"'
    And Microsoft Graph holds 2 Entra applications

  @op:azureIntegrations @op:azureIntegration @op:azureIntegrationByName @op:azureIntegrationCreate @op:azureIntegrationUpdate @op:azureIntegrationPatch @op:azureIntegrationDelete @op:azureIntegrationAttachedStack @op:azureIntegrationAttach @op:azureIntegrationAttachmentUpdate @op:azureIntegrationDetach @op:azureIntegrationConsentConfirm @op:azureIntegrationConsentClear @op:attachableAzureIntegrationsInSpace @op:stackAttachableAzureIntegrations @op:integrationsCount @case:failure
  Scenario: An Azure integration needs a sound tenant, a free name and a default subscription to auto-attach, and a stack holds one read and one write attachment
    When token "h" sends "POST /api/v1/azure-integrations" with '{"name":"","space":"{team}","tenantID":"acme","labels":[]}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/name" as "required"
    And the problem refuses the field "/tenantID" as "format"
    When token "h" sends "POST /api/v1/azure-integrations" with '{"name":"Auto","tenantID":"9a9b9c9d-0000-1111-2222-333344445555","labels":["autoattach:prod"],"autoattachEnabled":true}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/defaultSubscriptionId" as "rule"
    When token "h" sends "POST /api/v1/azure-integrations" with '{"name":"Lost","space":"nowhere","tenantID":"9a9b9c9d-0000-1111-2222-333344445555","labels":[]}' through A
    Then it answers a 404 problem "not-found"
    And Microsoft Graph holds 0 Entra applications
    Given token "h" sends "POST /api/v1/azure-integrations" with '{"name":"Deploy","space":"{team}","tenantID":"9a9b9c9d-0000-1111-2222-333344445555","labels":[]}' through B
    And the answer's id names "deploy"
    When token "h" sends "POST /api/v1/azure-integrations" with '{"name":"Deploy","space":"legacy","tenantID":"9a9b9c9d-0000-1111-2222-333344445555","labels":[]}' through A
    Then it answers a 409 problem "already-exists"
    And Microsoft Graph holds 1 Entra application
    When token "h" sends "PUT /api/v1/azure-integrations/{deploy}" with '{"name":"Deploy","tenantID":"00000000-0000-0000-0000-000000000000","labels":[]}' through B
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "PATCH /api/v1/azure-integrations/{deploy}" with '{"autoattachEnabled":true}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/defaultSubscriptionId" as "rule"
    When token "h" sends "GET /api/v1/azure-integrations?cursor=!!" through B
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "GET /api/v1/azure-integrations/01ARZ3NDEKTSV4RRFFQ69G5FAV" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/azure-integration-by-name?name=Nope" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "DELETE /api/v1/azure-integrations/01ARZ3NDEKTSV4RRFFQ69G5FAV" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/azure-integrations/01ARZ3NDEKTSV4RRFFQ69G5FAV/consent-confirm" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/azure-integrations/01ARZ3NDEKTSV4RRFFQ69G5FAV/consent-clear" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "PUT /api/v1/stacks/infra/azure-integrations/{deploy}" with '{"read":false,"write":false}' through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "PUT /api/v1/stacks/infra/azure-integrations/{deploy}" with '{"read":true,"write":false,"subscriptionId":"sub"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/subscriptionId" as "format"
    When token "h" sends "PUT /api/v1/stacks/gone/azure-integrations/{deploy}" with '{"read":true,"write":false}' through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "PUT /api/v1/stacks/infra/azure-integrations/{deploy}" with '{"read":true,"write":false}' through A
    Then it answers 200
    And the answer's id names "att"
    When token "h" sends "PUT /api/v1/stacks/infra/azure-integrations/{deploy}" with '{"read":false,"write":true}' through B
    Then it answers a 409 problem "invalid-state"
    When token "h" sends "PUT /api/v1/azure-integration-attachments/01ARZ3NDEKTSV4RRFFQ69G5FAV" with '{"read":true,"write":false}' through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "PUT /api/v1/azure-integration-attachments/{att}" with '{"read":false,"write":false}' through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "DELETE /api/v1/azure-integration-attachments/01ARZ3NDEKTSV4RRFFQ69G5FAV" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/azure-integrations/{deploy}/attached-stack?id=nope" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/attachable-azure-integrations-in-space?id=nowhere" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/stacks/gone/attachable-azure-integrations" through B
    Then it answers a 404 problem "not-found"
    When a caller with no token sends "GET /api/v1/integrations-count" through A
    Then it answers a 401 problem "unauthenticated"

  @op:azureIntegrationCreate @op:azureIntegrationDelete @op:azureIntegrations @case:failure
  Scenario: Graph's throttling is retried, and a Graph that stays down leaves neither an integration nor an application
    Given Microsoft Graph throttles the next 2 requests
    When token "h" sends "POST /api/v1/azure-integrations" with '{"name":"Patient","tenantID":"9a9b9c9d-0000-1111-2222-333344445555","labels":[]}' through A
    Then it answers 201
    And the answer's id names "patient"
    And Microsoft Graph was throttled and answered on a retry
    And Microsoft Graph holds 1 Entra application
    Given Microsoft Graph is unavailable
    When token "h" sends "POST /api/v1/azure-integrations" with '{"name":"Down","tenantID":"9a9b9c9d-0000-1111-2222-333344445555","labels":[]}' through B
    Then it answers a 503 problem "service-unavailable"
    When token "h" sends "DELETE /api/v1/azure-integrations/{patient}" through A
    Then it answers a 503 problem "service-unavailable"
    When token "h" sends "GET /api/v1/azure-integrations" through B
    Then the answer lists the Azure integrations "Patient"
    And Microsoft Graph holds 1 Entra application
    Given Microsoft Graph is available again
    When token "h" sends "DELETE /api/v1/azure-integrations/{patient}" through A
    Then it answers 200
    And Microsoft Graph holds 0 Entra applications
