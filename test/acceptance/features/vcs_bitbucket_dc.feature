@postgres @bitbucket-dc
Feature: Bitbucket Data Center integrations
  An account reaches a Bitbucket Data Center server with an HTTP access
  token, which it never reads back, and the username git checks out as. A
  stack on Bitbucket Data Center names its repository by its project's key
  and its slug, and reads it through the integration it names or its
  provider's default. The scenarios' server is an in-process fake at
  {bitbucketDCHost}. The home key is space-admin on the account's top
  space; the member key holds only the bindings a scenario gives it. Space
  team inherits from the top space.

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And a Bitbucket Data Center user "dev" with an access token kept as "tok"
    And a Bitbucket Data Center repository "ACME/infra" that "dev" administers, its head of "main" kept as "seed"

  @op:bitbucketDatacenterIntegrationCreate @op:bitbucketDatacenterIntegration @op:bitbucketDatacenterIntegrationUpdate @op:bitbucketDatacenterIntegrationTest @op:bitbucketDatacenterIntegrationDelete @op:vcsIntegration @case:success
  Scenario: An admin connects Bitbucket Data Center by a token, tests, reads, updates and deletes it
    When token "h" sends "POST /api/v1/bitbucket-datacenter-integrations" with '{"apiHost":"{bitbucketDCHost}","userFacingHost":"{bitbucketDCHost}","accessToken":"{tok}","username":"dev","customInput":{"name":"DC","spaceID":"{team}","labels":["infra"]}}' through A
    Then it answers 201
    And the answer's id names "d"
    And the answer's username is '"dev"'
    And the answer's webhookURL is '"http://infraweave.test/webhooks/vcs/bitbucket_datacenter/{d}"'
    When token "h" sends "POST /api/v1/bitbucket-datacenter-integrations/{d}/test" through B
    Then it answers 200
    And the answer is 'true'
    When token "h" sends "GET /api/v1/bitbucket-datacenter-integrations/{d}" through B
    Then it answers 200
    And the answer's name is '"DC"'
    And the answer's labels is '["infra"]'
    And the answer leaves out "accessToken"
    When token "h" sends "GET /api/v1/vcs-integrations/{d}?provider=BITBUCKET_DATACENTER" through A
    Then it answers 200
    When token "h" sends "PUT /api/v1/bitbucket-datacenter-integrations/{d}" with '{"apiHost":"{bitbucketDCHost}","userFacingHost":"https://bitbucket.example","customInput":{"space":"{team}","description":"The team server","vcsChecks":"AGGREGATED"}}' through A
    Then it answers 200
    And the answer's description is '"The team server"'
    And the answer's userFacingHost is '"https://bitbucket.example"'
    And the answer's username is '"dev"'
    And the answer's vcsChecks is '"AGGREGATED"'
    When token "h" sends "POST /api/v1/bitbucket-datacenter-integrations/{d}/test" through B
    Then the answer is 'true'
    When token "h" sends "DELETE /api/v1/bitbucket-datacenter-integrations/{d}" through B
    Then it answers 200
    When token "h" sends "GET /api/v1/bitbucket-datacenter-integrations/{d}" through A
    Then it answers a 404 problem "not-found"

  @op:bitbucketDatacenterIntegrationCreate @op:stackCreate @op:stackSyncCommit @case:success
  Scenario: A stack on a project's repository tracks its branch through its provider's default
    When token "h" sends "POST /api/v1/bitbucket-datacenter-integrations" with '{"apiHost":"{bitbucketDCHost}","userFacingHost":"{bitbucketDCHost}","accessToken":"{tok}","customInput":{"name":"Main","spaceID":"acme","isDefault":true}}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","namespace":"ACME","repository":"infra","branch":"main","provider":"BITBUCKET_DATACENTER","space":"{team}"},"manageState":false}' through B
    Then it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    When a commit to "main" of the Bitbucket Data Center repository "ACME/infra" is kept as "pushed"
    And token "h" sends "POST /api/v1/stacks/infra/sync-commit" through B
    Then it answers 200
    And the answer tracks the commit kept as "pushed" at the branch head kept as "pushed"

  @op:bitbucketDatacenterIntegrationCreate @op:bitbucketDatacenterIntegration @op:bitbucketDatacenterIntegrationUpdate @op:bitbucketDatacenterIntegrationDelete @op:bitbucketDatacenterIntegrationTest @case:denial
  Scenario: A reader of a space sees its integrations without their secrets, and changes nothing
    Given the member key is space-reader on the space "{team}"
    When token "h" sends "POST /api/v1/bitbucket-datacenter-integrations" with '{"apiHost":"{bitbucketDCHost}","userFacingHost":"{bitbucketDCHost}","accessToken":"{tok}","customInput":{"name":"DC","spaceID":"{team}"}}' through A
    Then the answer's id names "d"
    When token "m" sends "GET /api/v1/bitbucket-datacenter-integrations/{d}" through B
    Then it answers 200
    And the answer's webhookSecret is '""'
    And the answer's webhookURL is '""'
    When token "m" sends "POST /api/v1/bitbucket-datacenter-integrations" with '{"apiHost":"{bitbucketDCHost}","userFacingHost":"{bitbucketDCHost}","accessToken":"{tok}","customInput":{"name":"Mine","spaceID":"{team}"}}' through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PUT /api/v1/bitbucket-datacenter-integrations/{d}" with '{"apiHost":"{bitbucketDCHost}","userFacingHost":"{bitbucketDCHost}","customInput":{"space":"{team}"}}' through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "DELETE /api/v1/bitbucket-datacenter-integrations/{d}" through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/bitbucket-datacenter-integrations/{d}/test" through B
    Then it answers a 403 problem "forbidden"

  @op:bitbucketDatacenterIntegrationCreate @op:bitbucketDatacenterIntegrationTest @op:bitbucketDatacenterIntegrationUpdate @op:stackSyncCommit @case:failure
  Scenario: A bad host, a refused token, another space and a stack with no integration are refused
    When token "h" sends "POST /api/v1/bitbucket-datacenter-integrations" with '{"apiHost":"http://bitbucket.example","userFacingHost":"https://bitbucket.example","accessToken":"t","customInput":{"name":"Bad","spaceID":"acme"}}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/apiHost"
    When token "h" sends "POST /api/v1/bitbucket-datacenter-integrations" with '{"apiHost":"{bitbucketDCHost}","userFacingHost":"{bitbucketDCHost}","accessToken":"revoked","customInput":{"name":"Revoked","spaceID":"acme"}}' through B
    Then it answers 201
    And the answer's id names "r"
    When token "h" sends "POST /api/v1/bitbucket-datacenter-integrations/{r}/test" through A
    Then it answers 200
    And the answer is 'false'
    When token "h" sends "PUT /api/v1/bitbucket-datacenter-integrations/{r}" with '{"apiHost":"{bitbucketDCHost}","userFacingHost":"{bitbucketDCHost}","customInput":{"space":"{team}"}}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/customInput/space"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Orphan","namespace":"ACME","repository":"infra","branch":"main","provider":"BITBUCKET_DATACENTER","space":"{team}"},"manageState":false}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/stacks/orphan/sync-commit" through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/vcsIntegrationId"
