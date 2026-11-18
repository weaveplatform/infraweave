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
    And the answer's ETag is kept as "updated"
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

  @op:bitbucketDatacenterIntegrationCreate @op:stackCreate @op:stackSyncCommit @op:stackSetCurrentCommit @case:success
  Scenario: A stack on a project's repository tracks its branch through its provider's default, and pins an earlier commit
    When token "h" sends "POST /api/v1/bitbucket-datacenter-integrations" with '{"apiHost":"{bitbucketDCHost}","userFacingHost":"{bitbucketDCHost}","accessToken":"{tok}","customInput":{"name":"Main","spaceID":"acme","isDefault":true}}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","namespace":"ACME","repository":"infra","branch":"main","provider":"BITBUCKET_DATACENTER","space":"{team}"},"manageState":false}' through B
    Then it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    When a commit to "main" of the Bitbucket Data Center repository "ACME/infra" is kept as "pushed"
    And token "h" sends "POST /api/v1/stacks/infra/sync-commit" through B
    Then it answers 200
    And the answer tracks the commit kept as "pushed" at the branch head kept as "pushed"
    When token "h" sends "POST /api/v1/stacks/infra/set-current-commit" with '{"sha":"{seed}"}' through A
    Then it answers 200
    And the answer tracks the commit kept as "seed" at the branch head kept as "pushed"

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

  @op:bitbucketDatacenterIntegrationCreate @op:repositories @op:branches @case:success
  Scenario: A user pages the repositories and branches of the provider's default
    Given a Bitbucket Data Center repository "ACME/web" that "dev" administers, its head of "main" kept as "web"
    And a commit to "dev" of the Bitbucket Data Center repository "ACME/infra" is kept as "dev"
    When token "h" sends "POST /api/v1/bitbucket-datacenter-integrations" with '{"apiHost":"{bitbucketDCHost}","userFacingHost":"{bitbucketDCHost}","accessToken":"{tok}","customInput":{"name":"Main","spaceID":"acme","isDefault":true}}' through A
    Then it answers 201
    When token "h" sends "GET /api/v1/repositories?provider=BITBUCKET_DATACENTER&limit=1" through B
    Then it answers 200
    And the answer's items is '[{"namespace":"ACME","id":"infra"}]'
    And the answer's nextCursor is kept as "next"
    When token "h" sends "GET /api/v1/repositories?provider=BITBUCKET_DATACENTER&limit=1&cursor={next}" through A
    Then it answers 200
    And the answer's items is '[{"namespace":"ACME","id":"web"}]'
    And the answer leaves out "nextCursor"
    When token "h" sends "GET /api/v1/branches?provider=BITBUCKET_DATACENTER&namespace=ACME&repository=infra&limit=1" through B
    Then it answers 200
    And the answer's items is '["dev"]'
    And the answer's nextCursor is kept as "next"
    When token "h" sends "GET /api/v1/branches?provider=BITBUCKET_DATACENTER&namespace=ACME&repository=infra&limit=1&cursor={next}" through A
    Then it answers 200
    And the answer's items is '["main"]'
    And the answer leaves out "nextCursor"

  @op:bitbucketDatacenterIntegration @op:bitbucketDatacenterIntegrationDelete @case:failure
  Scenario: A missing Bitbucket Data Center integration cannot be read or deleted, and deletion succeeds only once
    Given the member key is space-reader on the space "{team}"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Secret","description":"","inheritEntities":false,"parentSpace":"acme"}' through B
    And the answer's id is the space "secret"
    When token "h" sends "GET /api/v1/bitbucket-datacenter-integrations/missing-integration" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "DELETE /api/v1/bitbucket-datacenter-integrations/missing-integration" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/bitbucket-datacenter-integrations" with '{"apiHost":"{bitbucketDCHost}","userFacingHost":"{bitbucketDCHost}","accessToken":"{tok}","username":"dev","customInput":{"name":"Evidence","spaceID":"{secret}","labels":["infra"]}}' through A
    Then it answers 201
    And the answer's id names "hidden"
    When token "h" sends "GET /api/v1/bitbucket-datacenter-integrations/{hidden}" through A
    Then it answers 200
    And the answer's name is '"Evidence"'
    When token "h" sends "DELETE /api/v1/bitbucket-datacenter-integrations/{hidden}" through B
    Then it answers 200
    When token "h" sends "DELETE /api/v1/bitbucket-datacenter-integrations/{hidden}" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/bitbucket-datacenter-integrations/{hidden}" through B
    Then it answers a 404 problem "not-found"

  @op:bitbucketDatacenterIntegration @op:vcsIntegrationTest @case:denial
  Scenario: A Bitbucket Data Center integration in an unreadable space is hidden from reads and connection tests
    Given the member key is space-reader on the space "{team}"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Secret","description":"","inheritEntities":false,"parentSpace":"acme"}' through B
    And the answer's id is the space "secret"
    And token "h" sends "POST /api/v1/bitbucket-datacenter-integrations" with '{"apiHost":"{bitbucketDCHost}","userFacingHost":"{bitbucketDCHost}","accessToken":"{tok}","username":"dev","customInput":{"name":"Evidence","spaceID":"{secret}","labels":["infra"]}}' through A
    And it answers 201
    And the answer's id names "hidden"
    When token "m" sends "GET /api/v1/bitbucket-datacenter-integrations/{hidden}" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/vcs-integrations/{hidden}/test?provider=BITBUCKET_DATACENTER" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/bitbucket-datacenter-integrations/{hidden}" through B
    Then it answers 200
    And the answer's name is '"Evidence"'

  @op:bitbucketDatacenterIntegrationPatch @op:vcsIntegrationTest @case:success
  Scenario: A Bitbucket Data Center merge patch changes one member, preserves the rest and returns a new ETag
    Given token "h" sends "POST /api/v1/bitbucket-datacenter-integrations" with '{"apiHost":"{bitbucketDCHost}","userFacingHost":"{bitbucketDCHost}","accessToken":"{tok}","username":"dev","customInput":{"name":"Evidence","spaceID":"{team}","labels":["infra"],"useGitCheckout":true,"vcsChecks":"ALL","vcsCheckOptions":{"individualFailuresOnly":true,"individualSkipUnchanged":true}}}' through A
    And it answers 201
    And the answer's id names "probe"
    And token "h" sends "GET /api/v1/bitbucket-datacenter-integrations/{probe}" through B
    And it answers 200
    And the answer's ETag is kept as "before"
    When token "h" sends "PATCH /api/v1/bitbucket-datacenter-integrations/{probe}" with '{"customInput":{"description":"Patched description"}}' if matching the ETag "before" through A
    Then it answers 200
    And the answer's ETag differs from "before"
    And the answer's ETag is kept as "after"
    And the answer's description is '"Patched description"'
    And the answer's name is '"Evidence"'
    And the answer's labels is '["infra"]'
    And the answer's vcsChecks is '"ALL"'
    And the answer's vcsCheckOptions is '{"individualFailuresOnly":true,"individualSkipUnchanged":true}'
    And the answer's useGitCheckout is 'true'
    And the answer's username is '"dev"'
    When token "h" sends "GET /api/v1/bitbucket-datacenter-integrations/{probe}" through B
    Then it answers 200
    And the answer's description is '"Patched description"'
    And the answer's labels is '["infra"]'
    When token "h" sends "GET /api/v1/vcs-integrations/{probe}/test?provider=BITBUCKET_DATACENTER" through B
    Then it answers 200
    And the answer's status is '"SUCCESS"'
    When token "h" sends "PATCH /api/v1/bitbucket-datacenter-integrations/{probe}" with '{"accessToken":null,"customInput":{"description":null,"labels":null,"vcsChecks":null,"useGitCheckout":null,"vcsCheckOptions":null}}' if matching the ETag "after" through A
    Then it answers 200
    And the answer's ETag differs from "after"
    And the answer's description is '""'
    And the answer's labels is '[]'
    And the answer's vcsChecks is '"INDIVIDUAL"'
    And the answer's vcsCheckOptions is '{"individualFailuresOnly":false,"individualSkipUnchanged":false}'
    And the answer's useGitCheckout is 'false'
    When token "h" sends "GET /api/v1/vcs-integrations/{probe}/test?provider=BITBUCKET_DATACENTER" through B
    Then it answers 200
    And the answer's status is '"SUCCESS"'

  @op:bitbucketDatacenterIntegrationPatch @case:denial
  Scenario: A Bitbucket Data Center reader cannot merge patch an integration it can read
    Given the member key is space-reader on the space "{team}"
    And token "h" sends "POST /api/v1/bitbucket-datacenter-integrations" with '{"apiHost":"{bitbucketDCHost}","userFacingHost":"{bitbucketDCHost}","accessToken":"{tok}","username":"dev","customInput":{"name":"Evidence","spaceID":"{team}","labels":["infra"]}}' through A
    And it answers 201
    And the answer's id names "probe"
    When token "m" sends "GET /api/v1/bitbucket-datacenter-integrations/{probe}" through B
    Then it answers 200
    When token "m" sends "PATCH /api/v1/bitbucket-datacenter-integrations/{probe}" with '{"customInput":{"description":"Forbidden description"}}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_ADMIN"'
    When token "h" sends "GET /api/v1/bitbucket-datacenter-integrations/{probe}" through B
    Then it answers 200
    And the answer's description is '""'
    And the answer's labels is '["infra"]'

  @op:bitbucketDatacenterIntegrationPatch @case:failure
  Scenario: A Bitbucket Data Center merge patch refuses a stale ETag and unknown members without changing the integration
    Given token "h" sends "POST /api/v1/bitbucket-datacenter-integrations" with '{"apiHost":"{bitbucketDCHost}","userFacingHost":"{bitbucketDCHost}","accessToken":"{tok}","username":"dev","customInput":{"name":"Evidence","spaceID":"{team}","labels":["infra"]}}' through A
    And it answers 201
    And the answer's id names "probe"
    And token "h" sends "GET /api/v1/bitbucket-datacenter-integrations/{probe}" through B
    And it answers 200
    And the answer's ETag is kept as "before"
    When token "h" sends "PATCH /api/v1/bitbucket-datacenter-integrations/{probe}" with '{"customInput":{"description":"Current description"}}' if matching the ETag "before" through A
    Then it answers 200
    And the answer's ETag differs from "before"
    And the answer's ETag is kept as "current"
    When token "h" sends "PATCH /api/v1/bitbucket-datacenter-integrations/{probe}" with '{"customInput":{"description":"Stale description"}}' if matching the ETag "before" through B
    Then it answers a 412 problem "precondition-failed"
    When token "h" sends "PATCH /api/v1/bitbucket-datacenter-integrations/{probe}" with '{"unrecognizedSetting":true}' if matching the ETag "current" through A
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "PATCH /api/v1/bitbucket-datacenter-integrations/{probe}" with '{"customInput":{"unrecognizedSetting":true}}' if matching the ETag "current" through B
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "GET /api/v1/bitbucket-datacenter-integrations/{probe}" through A
    Then it answers 200
    And the answer's description is '"Current description"'
    And the answer's labels is '["infra"]'
    When token "h" sends "PATCH /api/v1/bitbucket-datacenter-integrations/{probe}" with '{"customInput":{"description":"Final description"}}' if matching the ETag "current" through B
    Then it answers 200
    And the answer's description is '"Final description"'

  @op:bitbucketDatacenterIntegrationRegenerateWebhookSecret @op:bitbucketDatacenterWebhooksEndpoint @case:success @webhook-controls
  Scenario: A Bitbucket Data Center admin rotates secrets once per key and reads the configured delivery URL
    When token "h" sends "POST /api/v1/bitbucket-datacenter-integrations" with '{"apiHost":"{bitbucketDCHost}","userFacingHost":"{bitbucketDCHost}","accessToken":"{tok}","username":"dev","customInput":{"name":"Webhook control","spaceID":"acme","isDefault":true}}' through A
    Then it answers 201
    And the answer's id names "hookIntegration"
    And the answer's webhook secret is kept as "originalHook"
    When token "h" sends "GET /api/v1/bitbucket-datacenter-webhooks-endpoint" through B
    Then it answers 200
    And the answer is '"http://infraweave.test/webhooks/vcs/bitbucket_datacenter/{hookIntegration}"'
    When token "h" regenerates the webhook secret at "/api/v1/bitbucket-datacenter-integrations/{hookIntegration}/regenerate-webhook-secret" with idempotency key "webhook-control-first" through A
    Then it answers 200
    And the answer's webhook secret differs from "originalHook"
    And the answer's webhook secret is kept as "firstHook"
    When token "h" sends "GET /api/v1/bitbucket-datacenter-integrations/{hookIntegration}" through B
    Then it answers 200
    And the answer's webhookSecret is '"{firstHook}"'
    When token "h" regenerates the webhook secret at "/api/v1/bitbucket-datacenter-integrations/{hookIntegration}/regenerate-webhook-secret" with idempotency key "webhook-control-first" through B
    Then it answers 200
    And the answer's webhookSecret is '"{firstHook}"'
    And integration "hookIntegration" has 1 secret rotation audit entries and never logs or audits secrets "originalHook, firstHook"
    When token "h" sends "POST /api/v1/bitbucket-datacenter-integrations/{hookIntegration}/regenerate-webhook-secret" through B
    Then it answers 200
    And the answer's webhook secret differs from "firstHook"
    And the answer's webhook secret is kept as "secondHook"
    When token "h" sends "GET /api/v1/bitbucket-datacenter-integrations/{hookIntegration}" through A
    Then it answers 200
    And the answer's webhookSecret is '"{secondHook}"'
    And integration "hookIntegration" has 2 secret rotation audit entries and never logs or audits secrets "originalHook, firstHook, secondHook"

  @op:bitbucketDatacenterIntegrationRegenerateWebhookSecret @op:bitbucketDatacenterWebhooksEndpoint @case:denial @webhook-controls
  Scenario: A Bitbucket Data Center space reader cannot rotate a secret or read the delivery URL
    Given the member key is space-reader on the space "acme"
    And token "h" sends "POST /api/v1/bitbucket-datacenter-integrations" with '{"apiHost":"{bitbucketDCHost}","userFacingHost":"{bitbucketDCHost}","accessToken":"{tok}","username":"dev","customInput":{"name":"Protected webhook","spaceID":"acme","isDefault":true}}' through A
    And it answers 201
    And the answer's id names "hookIntegration"
    And the answer's webhook secret is kept as "untouchedHook"
    When token "m" sends "GET /api/v1/bitbucket-datacenter-webhooks-endpoint" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_ADMIN"'
    When token "m" sends "POST /api/v1/bitbucket-datacenter-integrations/{hookIntegration}/regenerate-webhook-secret" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_ADMIN"'
    When token "h" sends "GET /api/v1/bitbucket-datacenter-integrations/{hookIntegration}" through B
    Then it answers 200
    And the answer's webhookSecret is '"{untouchedHook}"'
    And integration "hookIntegration" has 0 secret rotation audit entries and never logs or audits secrets "untouchedHook"

  @op:bitbucketDatacenterIntegrationRegenerateWebhookSecret @op:bitbucketDatacenterWebhooksEndpoint @case:failure @webhook-controls
  Scenario: Bitbucket Data Center webhook controls refuse missing defaults and missing or hidden integrations
    Given the member key is space-reader on the space "{team}"
    When token "h" sends "GET /api/v1/bitbucket-datacenter-webhooks-endpoint" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/bitbucket-datacenter-integrations/missing-integration/regenerate-webhook-secret" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/spaces" with '{"name":"Private hooks","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    Then it answers 201
    And the answer's id is the space "privateHooks"
    When token "h" sends "POST /api/v1/bitbucket-datacenter-integrations" with '{"apiHost":"{bitbucketDCHost}","userFacingHost":"{bitbucketDCHost}","accessToken":"{tok}","username":"dev","customInput":{"name":"Hidden hook","spaceID":"{privateHooks}"}}' through B
    Then it answers 201
    And the answer's id names "hookIntegration"
    When token "m" sends "POST /api/v1/bitbucket-datacenter-integrations/{hookIntegration}/regenerate-webhook-secret" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "DELETE /api/v1/bitbucket-datacenter-integrations/{hookIntegration}" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/bitbucket-datacenter-integrations/{hookIntegration}/regenerate-webhook-secret" through A
    Then it answers a 404 problem "not-found"

  @op:vcsIntegrationTest @case:failure @webhook-controls
  Scenario: A Bitbucket Data Center connection test checks the delivery URL and every consumed hook event
    Given token "h" sends "POST /api/v1/bitbucket-datacenter-integrations" with '{"apiHost":"{bitbucketDCHost}","userFacingHost":"{bitbucketDCHost}","accessToken":"{tok}","username":"dev","customInput":{"name":"Hook check","spaceID":"acme","isDefault":true}}' through A
    And it answers 201
    And the answer's id names "hookIntegration"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Hook source","namespace":"ACME","repository":"infra","branch":"main","provider":"BITBUCKET_DATACENTER","vcsIntegrationId":"{hookIntegration}","space":"{team}"},"manageState":false}' through B
    And it answers 201
    And the Bitbucket Data Center repository "ACME/infra" has no webhook
    When token "h" sends "GET /api/v1/vcs-integrations/{hookIntegration}/test?provider=BITBUCKET_DATACENTER" through A
    Then it answers 200
    And the answer's WEBHOOK check is FAIL
    And the failed webhook check explains "ACME/infra" against "http://infraweave.test/webhooks/vcs/bitbucket_datacenter/{hookIntegration}"
    When the Bitbucket Data Center repository "ACME/infra" has a webhook to "https://elsewhere.invalid/hooks" for events "repo:refs_changed, pr:opened, pr:from_ref_updated, pr:modified, pr:reviewer:approved, pr:reviewer:unapproved, pr:merged, pr:comment:added"
    And token "h" sends "GET /api/v1/vcs-integrations/{hookIntegration}/test?provider=BITBUCKET_DATACENTER" through B
    Then it answers 200
    And the answer's WEBHOOK check is FAIL
    And the failed webhook check explains "ACME/infra" against "http://infraweave.test/webhooks/vcs/bitbucket_datacenter/{hookIntegration}"
    When the Bitbucket Data Center repository "ACME/infra" has a webhook to "http://infraweave.test/webhooks/vcs/bitbucket_datacenter/{hookIntegration}" for events "repo:refs_changed"
    And token "h" sends "GET /api/v1/vcs-integrations/{hookIntegration}/test?provider=BITBUCKET_DATACENTER" through A
    Then it answers 200
    And the answer's WEBHOOK check is FAIL
    And the failed webhook check explains "ACME/infra" against "http://infraweave.test/webhooks/vcs/bitbucket_datacenter/{hookIntegration}"
    When the Bitbucket Data Center repository "ACME/infra" has a webhook to "http://infraweave.test/webhooks/vcs/bitbucket_datacenter/{hookIntegration}" for events "repo:refs_changed, pr:opened, pr:from_ref_updated, pr:modified, pr:reviewer:approved, pr:reviewer:unapproved, pr:merged, pr:comment:added"
    And token "h" sends "GET /api/v1/vcs-integrations/{hookIntegration}/test?provider=BITBUCKET_DATACENTER" through B
    Then it answers 200
    And the answer's WEBHOOK check is SUCCESS
    And the answer's status is '"SUCCESS"'
