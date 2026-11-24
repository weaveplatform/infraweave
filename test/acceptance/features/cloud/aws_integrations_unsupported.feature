@postgres
Feature: AWS integrations without AWS credentials
  An installation whose servers hold no AWS credentials stores AWS
  integrations whose private workers assume their roles, and refuses what
  needs its own AWS identity as installation-unsupported: its AWS account,
  an external ID's trust policy statement and the trust check of an
  attachment the server would assume. An installation that switches the
  cloud-integrations feature off answers that AWS integrations are off,
  and refuses their operations.

  @op:installationAwsAccountId @op:awsIntegrationExternalIdForStack @op:awsIntegrationAttach @op:awsIntegrationCreate @case:failure
  Scenario: Without AWS credentials the server serves only what a private worker assumes
    Given the servers hold no AWS credentials
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"provider":"GIT","name":"Infra","repository":"infra","branch":"main","space":"acme"},"manageState":false}' through A
    When token "h" sends "GET /api/v1/installation-aws-account-id" through A
    Then it answers a 403 problem "installation-unsupported"
    When token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Server","roleArn":"arn:aws:iam::222222222222:role/server","generateCredentialsInWorker":false,"labels":[]}' through B
    Then it answers 201
    And the answer's id names "server"
    When token "h" sends "PUT /api/v1/stacks/infra/aws-integrations/{server}" with '{"read":true,"write":false}' through A
    Then it answers a 403 problem "installation-unsupported"
    When token "h" sends "GET /api/v1/aws-integrations/{server}/external-id-for-stack?stackId=infra&read=true&write=false" through B
    Then it answers a 403 problem "installation-unsupported"
    When token "h" sends "POST /api/v1/aws-integrations" with '{"name":"Worker","roleArn":"arn:aws:iam::222222222222:role/worker","generateCredentialsInWorker":true,"labels":[]}' through A
    Then the answer's id names "worker"
    When token "h" sends "PUT /api/v1/stacks/infra/aws-integrations/{worker}" with '{"read":true,"write":true}' through B
    Then it answers 200

  @op:awsIntegrationsEnabled @op:awsIntegrations @case:failure
  Scenario: An installation that switches cloud integrations off answers that they are off
    Given a running infraweave server whose features.disabled is "cloud-integrations"
    And the home key is exchanged through A for token "h"
    When token "h" sends "GET /api/v1/aws-integrations-enabled" through A
    Then the answer is 'false'
    When token "h" sends "GET /api/v1/aws-integrations" through A
    Then it answers a 403 problem "feature-disabled"
    And the problem names the feature "cloud-integrations"
