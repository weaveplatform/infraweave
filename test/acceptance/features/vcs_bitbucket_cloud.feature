@postgres @bitbucket-cloud
Feature: Bitbucket Cloud integrations
  An account reaches Bitbucket Cloud with an Atlassian account's email and
  an API token, which it never reads back, and always checks out with git.
  A stack on Bitbucket Cloud names its repository by its workspace and its
  slug, and reads it through the integration it names or its provider's
  default. The scenarios' Bitbucket Cloud is an in-process fake the
  servers reach through vcs.bitbucket_cloud.api_host. The home key is
  space-admin on the account's top space; the member key holds only the
  bindings a scenario gives it. Space team inherits from the top space.

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And a Bitbucket Cloud account "dev@acme.example" with an API token kept as "tok"
    And a Bitbucket Cloud repository "acme/infra" that "dev@acme.example" administers, its head of "main" kept as "seed"

  @op:bitbucketCloudIntegrationCreateV2 @op:bitbucketCloudIntegration @op:bitbucketCloudIntegrationUpdateV2 @op:bitbucketCloudIntegrationTest @op:bitbucketCloudIntegrationDelete @op:vcsIntegration @case:success
  Scenario: An admin connects Bitbucket Cloud by an API token, tests, reads, updates and deletes it
    When token "h" sends "POST /api/v1/bitbucket-cloud-integrations" with '{"email":"dev@acme.example","apiToken":"{tok}","customInput":{"name":"Cloud","spaceID":"{team}","labels":["infra"]}}' through A
    Then it answers 201
    And the answer's id names "c"
    And the answer's email is '"dev@acme.example"'
    And the answer's useGitCheckout is 'true'
    And the answer's webhookUrl is '"http://infraweave.test/webhooks/vcs/bitbucket_cloud/{c}"'
    When token "h" sends "POST /api/v1/bitbucket-cloud-integrations/{c}/test" through B
    Then it answers 200
    And the answer is 'true'
    When token "h" sends "GET /api/v1/bitbucket-cloud-integrations/{c}" through B
    Then it answers 200
    And the answer's name is '"Cloud"'
    And the answer's labels is '["infra"]'
    And the answer leaves out "apiToken"
    When token "h" sends "GET /api/v1/vcs-integrations/{c}?provider=BITBUCKET_CLOUD" through A
    Then it answers 200
    When token "h" sends "PUT /api/v1/bitbucket-cloud-integrations/{c}" with '{"email":"dev@acme.example","customInput":{"space":"{team}","description":"The team workspace","vcsChecks":"AGGREGATED"}}' through A
    Then it answers 200
    And the answer's description is '"The team workspace"'
    And the answer's vcsChecks is '"AGGREGATED"'
    When token "h" sends "POST /api/v1/bitbucket-cloud-integrations/{c}/test" through B
    Then the answer is 'true'
    When token "h" sends "DELETE /api/v1/bitbucket-cloud-integrations/{c}" through B
    Then it answers 200
    When token "h" sends "GET /api/v1/bitbucket-cloud-integrations/{c}" through A
    Then it answers a 404 problem "not-found"

  @op:bitbucketCloudIntegrationCreateV2 @op:stackCreate @op:stackSyncCommit @case:success
  Scenario: A stack on a workspace's repository tracks its branch through its provider's default
    When token "h" sends "POST /api/v1/bitbucket-cloud-integrations" with '{"email":"dev@acme.example","apiToken":"{tok}","customInput":{"name":"Main","spaceID":"acme","isDefault":true}}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","namespace":"acme","repository":"infra","branch":"main","provider":"BITBUCKET_CLOUD","space":"{team}"},"manageState":false}' through B
    Then it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    When a commit to "main" of the Bitbucket Cloud repository "acme/infra" is kept as "pushed"
    And token "h" sends "POST /api/v1/stacks/infra/sync-commit" through B
    Then it answers 200
    And the answer tracks the commit kept as "pushed" at the branch head kept as "pushed"

  @op:bitbucketCloudIntegrationCreateV2 @op:bitbucketCloudIntegration @op:bitbucketCloudIntegrationUpdateV2 @op:bitbucketCloudIntegrationDelete @op:bitbucketCloudIntegrationTest @case:denial
  Scenario: A reader of a space sees its integrations without their secrets, and changes nothing
    Given the member key is space-reader on the space "{team}"
    When token "h" sends "POST /api/v1/bitbucket-cloud-integrations" with '{"email":"dev@acme.example","apiToken":"{tok}","customInput":{"name":"Cloud","spaceID":"{team}"}}' through A
    Then the answer's id names "c"
    When token "m" sends "GET /api/v1/bitbucket-cloud-integrations/{c}" through B
    Then it answers 200
    And the answer's webhookSecret is '""'
    And the answer's webhookUrl is '""'
    When token "m" sends "POST /api/v1/bitbucket-cloud-integrations" with '{"email":"dev@acme.example","apiToken":"{tok}","customInput":{"name":"Mine","spaceID":"{team}"}}' through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PUT /api/v1/bitbucket-cloud-integrations/{c}" with '{"email":"dev@acme.example","customInput":{"space":"{team}"}}' through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "DELETE /api/v1/bitbucket-cloud-integrations/{c}" through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/bitbucket-cloud-integrations/{c}/test" through B
    Then it answers a 403 problem "forbidden"

  @op:bitbucketCloudIntegrationCreateV2 @op:bitbucketCloudIntegrationTest @op:bitbucketCloudIntegrationUpdateV2 @op:stackSyncCommit @case:failure
  Scenario: A bad email, a refused token, another space and a stack with no integration are refused
    When token "h" sends "POST /api/v1/bitbucket-cloud-integrations" with '{"email":"dev","apiToken":"t","customInput":{"name":"Bad","spaceID":"acme"}}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/email"
    When token "h" sends "POST /api/v1/bitbucket-cloud-integrations" with '{"email":"dev@acme.example","apiToken":"revoked","customInput":{"name":"Revoked","spaceID":"acme"}}' through B
    Then it answers 201
    And the answer's id names "r"
    When token "h" sends "POST /api/v1/bitbucket-cloud-integrations/{r}/test" through A
    Then it answers 200
    And the answer is 'false'
    When token "h" sends "PUT /api/v1/bitbucket-cloud-integrations/{r}" with '{"email":"dev@acme.example","customInput":{"space":"{team}"}}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/customInput/space"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Orphan","namespace":"acme","repository":"infra","branch":"main","provider":"BITBUCKET_CLOUD","space":"{team}"},"manageState":false}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/stacks/orphan/sync-commit" through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/vcsIntegrationId"

  @op:bitbucketCloudIntegrationCreate @op:repositories @op:branches @case:success
  Scenario: A user pages the repositories and branches of the provider's default
    Given a Bitbucket Cloud repository "acme/web" that "dev@acme.example" administers, its head of "main" kept as "web"
    And a commit to "dev" of the Bitbucket Cloud repository "acme/infra" is kept as "dev"
    When token "h" sends "POST /api/v1/bitbucket-cloud-integrations" with '{"email":"dev@acme.example","apiToken":"{tok}","customInput":{"name":"Main","spaceID":"acme","isDefault":true}}' through A
    Then it answers 201
    When token "h" sends "GET /api/v1/repositories?provider=BITBUCKET_CLOUD&limit=1" through B
    Then it answers 200
    And the answer's items is '[{"namespace":"acme","id":"infra"}]'
    And the answer's nextCursor is kept as "next"
    When token "h" sends "GET /api/v1/repositories?provider=BITBUCKET_CLOUD&limit=1&cursor={next}" through A
    Then it answers 200
    And the answer's items is '[{"namespace":"acme","id":"web"}]'
    And the answer leaves out "nextCursor"
    When token "h" sends "GET /api/v1/branches?provider=BITBUCKET_CLOUD&namespace=acme&repository=infra&limit=1" through B
    Then it answers 200
    And the answer's items is '["dev"]'
    And the answer's nextCursor is kept as "next"
    When token "h" sends "GET /api/v1/branches?provider=BITBUCKET_CLOUD&namespace=acme&repository=infra&limit=1&cursor={next}" through A
    Then it answers 200
    And the answer's items is '["main"]'
    And the answer leaves out "nextCursor"
