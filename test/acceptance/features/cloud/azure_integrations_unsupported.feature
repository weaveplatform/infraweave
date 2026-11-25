@postgres
Feature: Azure integrations without an Entra application
  An installation without cloud.azure has no application to create Entra
  applications with: it answers that Azure integrations are off and
  refuses a create as installation-unsupported. An installation that
  switches the cloud-integrations feature off answers that they are off
  too, and refuses their operations.

  @op:azureIntegrationsEnabled @op:azureIntegrationCreate @op:azureIntegrations @op:integrationsCount @case:failure
  Scenario: Without cloud.azure the server creates no Azure integration
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    When token "h" sends "GET /api/v1/azure-integrations-enabled" through A
    Then the answer is 'false'
    When token "h" sends "POST /api/v1/azure-integrations" with '{"name":"Prod","tenantID":"9a9b9c9d-0000-1111-2222-333344445555","labels":[]}' through B
    Then it answers a 403 problem "installation-unsupported"
    When token "h" sends "GET /api/v1/azure-integrations" through A
    Then the answer lists the Azure integrations ""
    When token "h" sends "GET /api/v1/integrations-count" through B
    Then the answer's azure is '0'

  @op:azureIntegrationsEnabled @op:azureIntegrations @case:failure
  Scenario: An installation that switches cloud integrations off answers that Azure integrations are off
    Given the installation registers an Entra application for Azure integrations
    And a running infraweave server whose features.disabled is "cloud-integrations"
    And the home key is exchanged through A for token "h"
    When token "h" sends "GET /api/v1/azure-integrations-enabled" through A
    Then the answer is 'false'
    When token "h" sends "GET /api/v1/azure-integrations" through A
    Then it answers a 403 problem "feature-disabled"
    And the problem names the feature "cloud-integrations"
