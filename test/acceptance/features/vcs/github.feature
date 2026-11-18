@postgres @github
Feature: GitHub integrations
  Each account reaches GitHub through its own Apps on github.com or a
  GitHub Enterprise Server, registered by key or through the manifest flow.
  GITHUB is the account-default App alias, and run credentials are narrowed
  to one repository and sealed to the worker pool.

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And a GitHub App "ent" installed on the organization "acme"

  @op:githubEnterpriseIntegrationCreate @op:githubEnterpriseIntegration @op:githubEnterpriseIntegrationUpdate @case:success
  Scenario: An admin reads and updates the account default App's check options
    When token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{entID}","privateKey":"{entKey}","customInput":{"name":"Default App","spaceID":"acme","isDefault":true}}' through A
    Then it answers 201
    And the answer's id names "defaultApp"
    When token "h" sends "GET /api/v1/github-enterprise-integrations/{defaultApp}" through A
    Then it answers 200
    And the answer's apiHost is '"{githubHost}"'
    And the answer's vcsChecks is '"INDIVIDUAL"'
    When token "h" sends "PUT /api/v1/github-enterprise-integrations/{defaultApp}" with '{"apiHost":"{githubHost}","webhookSecret":"hook","customInput":{"space":"acme","vcsChecks":"ALL","useGitCheckout":true,"vcsCheckOptions":{"individualFailuresOnly":true}}}' through B
    Then it answers 200
    And the answer's ETag is kept as "updated"
    And the answer's vcsChecks is '"ALL"'
    When token "h" sends "PUT /api/v1/github-enterprise-integrations/{defaultApp}" with '{"apiHost":"{githubHost}","webhookSecret":"hook","customInput":{"space":"acme","vcsChecks":"AGGREGATED"}}' through A
    Then it answers 200
    When token "h" sends "GET /api/v1/github-enterprise-integrations/{defaultApp}" through B
    Then the answer's vcsChecks is '"AGGREGATED"'
    And the answer's useGitCheckout is 'true'
    And the answer's vcsCheckOptions is '{"individualFailuresOnly":true,"individualSkipUnchanged":false}'

  @op:githubEnterpriseIntegrationCreate @op:githubEnterpriseIntegration @op:githubEnterpriseIntegrationUpdate @op:githubEnterpriseIntegrationTest @op:githubEnterpriseIntegrationDelete @op:vcsIntegration @case:success
  Scenario: An admin registers an App by its key, tests, reads, updates and deletes it
    When token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{entID}","privateKey":"{entKey}","customInput":{"name":"Enterprise","spaceID":"{team}","labels":["infra"]}}' through A
    Then it answers 201
    And the answer's id names "e"
    And the answer's githubAppStatus is '"OK"'
    And the answer's githubAppExists is 'true'
    And the answer's webhookUrl is '"http://infraweave.test/webhooks/vcs/github_enterprise/{e}"'
    When token "h" sends "POST /api/v1/github-enterprise-integrations/{e}/test" through B
    Then it answers 200
    And the answer is 'true'
    When token "h" sends "GET /api/v1/github-enterprise-integrations/{e}" through B
    Then it answers 200
    And the answer's name is '"Enterprise"'
    And the answer's appID is '"{entID}"'
    And the answer's userFacingHost is '"{githubHost}"'
    When token "h" sends "GET /api/v1/vcs-integrations/{e}?provider=GITHUB_ENTERPRISE" through A
    Then it answers 200
    When token "h" sends "PUT /api/v1/github-enterprise-integrations/{e}" with '{"apiHost":"{githubHost}","webhookSecret":"rotated","customInput":{"space":"{team}","description":"The team App"}}' through A
    Then it answers 200
    And the answer's description is '"The team App"'
    And the answer's webhookSecret is '"rotated"'
    And the answer's githubAppStatus is '"OK"'
    When token "h" sends "DELETE /api/v1/github-enterprise-integrations/{e}" through B
    Then it answers 200
    When token "h" sends "GET /api/v1/github-enterprise-integrations/{e}" through A
    Then it answers a 404 problem "not-found"

  @op:githubAppGenerateManifest @op:githubAppCreateFromManifest @case:success
  Scenario: An admin creates an App through a manifest form, which completes once
    When token "h" sends "POST /api/v1/github-app-manifests" with '{"ownerType":"ORG","ownerName":"acme","host":"{githubHost}","apiHost":"{githubHost}"}' through A
    Then it answers 200
    And the answer's manifest state is kept as "state"
    And the GitHub host hands back a manifest code kept as "code"
    When token "h" sends "POST /api/v1/github-enterprise-integrations/from-manifest" with '{"code":"{code}","state":"{state}"}' through B
    Then it answers 201
    And the answer's id is '"{state}"'
    And the answer's isDefault is 'true'
    And the answer's githubAppStatus is '"OK"'
    When token "h" sends "POST /api/v1/github-enterprise-integrations/from-manifest" with '{"code":"{code}","state":"{state}"}' through A
    Then it answers a 422 problem "validation-failed"

  @op:githubEnterpriseIntegrationCreate @op:githubEnterpriseIntegration @op:githubEnterpriseIntegrationUpdate @op:githubEnterpriseIntegrationDelete @op:githubEnterpriseIntegrationTest @op:githubAppGenerateManifest @op:githubAppCreateFromManifest @case:denial
  Scenario: A reader of a space sees its Apps without their secrets, and changes nothing
    Given the member key is space-reader on the space "{team}"
    When token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{entID}","privateKey":"{entKey}","customInput":{"name":"Enterprise","spaceID":"{team}"}}' through A
    Then the answer's id names "e"
    When token "m" sends "GET /api/v1/github-enterprise-integrations/{e}" through B
    Then it answers 200
    And the answer's webhookSecret is '""'
    And the answer's webhookUrl is '""'
    When token "m" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{entID}","privateKey":"{entKey}","customInput":{"name":"Mine","spaceID":"{team}"}}' through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PUT /api/v1/github-enterprise-integrations/{e}" with '{"apiHost":"{githubHost}","webhookSecret":"x","customInput":{"space":"{team}"}}' through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "DELETE /api/v1/github-enterprise-integrations/{e}" through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/github-enterprise-integrations/{e}/test" through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/github-app-manifests" with '{"ownerType":"USER"}' through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/github-enterprise-integrations/from-manifest" with '{"code":"x","state":"y"}' through A
    Then it answers a 403 problem "forbidden"

  @op:githubEnterpriseIntegrationCreate @op:githubEnterpriseIntegrationTest @op:githubEnterpriseIntegrationUpdate @op:githubAppGenerateManifest @op:githubAppCreateFromManifest @case:failure
  Scenario: Bad settings, an App that misses events, another space and an unknown form are refused
    When token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"http://github.example","appID":"seven","privateKey":"not a key","customInput":{"name":"Bad","spaceID":"acme"}}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/apiHost"
    And the problem refuses the field "/appID"
    And the problem refuses the field "/privateKey"
    Given the GitHub App "ent" subscribes to no events
    When token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{entID}","privateKey":"{entKey}","customInput":{"name":"Deaf","spaceID":"acme"}}' through B
    Then it answers 201
    And the answer's id names "d"
    When token "h" sends "POST /api/v1/github-enterprise-integrations/{d}/test" through A
    Then it answers 200
    And the answer is 'false'
    When token "h" sends "PUT /api/v1/github-enterprise-integrations/{d}" with '{"apiHost":"{githubHost}","webhookSecret":"x","customInput":{"space":"{team}"}}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/customInput/space"
    When token "h" sends "POST /api/v1/github-app-manifests" with '{"ownerType":"ORG"}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "POST /api/v1/github-enterprise-integrations/from-manifest" with '{"code":"x","state":"nope"}' through B
    Then it answers a 422 problem "validation-failed"

  @op:repositories @op:branches @case:success
  Scenario: A user pages the repositories and branches of the account's default App
    Given token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{entID}","privateKey":"{entKey}","customInput":{"name":"Default App","spaceID":"acme","isDefault":true}}' through A
    And it answers 201
    Given a commit to "main" of the GitHub repository "acme/infra" is kept as "first"
    And a commit to "dev" of the GitHub repository "acme/infra" is kept as "dev"
    When token "h" sends "GET /api/v1/repositories?provider=GITHUB&limit=1" through B
    Then it answers 200
    And the answer's items is '[{"namespace":"acme","id":"infra"}]'
    And the answer's nextCursor is kept as "next"
    When token "h" sends "GET /api/v1/repositories?provider=GITHUB&limit=1&cursor={next}" through A
    Then it answers 200
    And the answer's items is '[{"namespace":"acme","id":"web"}]'
    And the answer leaves out "nextCursor"
    When token "h" sends "GET /api/v1/branches?provider=GITHUB&namespace=acme&repository=infra&limit=1" through B
    Then it answers 200
    And the answer's items is '["dev"]'
    And the answer's nextCursor is kept as "next"
    When token "h" sends "GET /api/v1/branches?provider=GITHUB&namespace=acme&repository=infra&limit=1&cursor={next}" through A
    Then it answers 200
    And the answer's items is '["main"]'
    And the answer leaves out "nextCursor"

  @op:stackCreate @op:stackSyncCommit @op:stackSetCurrentCommit @case:success
  Scenario: A stack on a private repository tracks its branch through the account's default App, and pins an earlier commit
    Given token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{entID}","privateKey":"{entKey}","customInput":{"name":"Default App","spaceID":"acme","isDefault":true}}' through A
    And it answers 201
    Given a commit to "main" of the GitHub repository "acme/infra" is kept as "seed"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","namespace":"acme","repository":"infra","branch":"main","provider":"GITHUB","space":"{team}"},"manageState":false}' through B
    Then it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    When a commit to "main" of the GitHub repository "acme/infra" is kept as "pushed"
    And token "h" sends "POST /api/v1/stacks/infra/sync-commit" through B
    Then it answers 200
    And the answer tracks the commit kept as "pushed" at the branch head kept as "pushed"
    When token "h" sends "POST /api/v1/stacks/infra/set-current-commit" with '{"sha":"{seed}"}' through A
    Then it answers 200
    And the answer tracks the commit kept as "seed" at the branch head kept as "pushed"

  @op:stackCreate @op:runTrigger @case:success
  Scenario: A run of a stack on a private repository fetches its source by Git with a token sealed for that repository alone
    Given token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{entID}","privateKey":"{entKey}","customInput":{"name":"Default App","spaceID":"acme","isDefault":true}}' through A
    And it answers 201
    Given token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And a commit to "main" of the GitHub repository "acme/infra" is kept as "seed"
    And a certificate signing request for a P-256 key is kept as "csr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"{team}","certificateSigningRequest":"{csr}"}' through A
    And the answer's id names "pool"
    And the answer's pool token names the pool "pool" and chains to the pool CA, its serial kept as "cert"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","namespace":"acme","repository":"infra","branch":"main","provider":"GITHUB","space":"{team}","workerPool":"{pool}"},"manageState":false}' through B
    And the launcher "l" registers with the key of "csr" and the certificate "cert" through A
    When token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through B
    And the answer's id names "run"
    And token "h" eventually reads the run "run" in the state "READY" through A
    And token "l" sends "POST /worker/v1/poll" with '{"busy":false,"wait":5}' through B
    Then the answer assigns the run "run", its run token kept as "rt"
    When token "rt" sends "GET /worker/v1/runs/{run}/spec" through A
    Then it answers 200
    And the specification fetches its source by Git from "{githubHost}/acme/infra.git", narrowed
    And the specification's sealed part opens with the key of "csr" to the source credential of "x-access-token", whose token neither the answer nor a server's log names

  @op:githubEnterpriseIntegration @op:githubEnterpriseIntegrationDelete @case:failure
  Scenario: A missing GitHub App integration cannot be read or deleted, and deletion succeeds only once
    Given the member key is space-reader on the space "{team}"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Secret","description":"","inheritEntities":false,"parentSpace":"acme"}' through B
    And the answer's id is the space "secret"
    When token "h" sends "GET /api/v1/github-enterprise-integrations/missing-integration" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "DELETE /api/v1/github-enterprise-integrations/missing-integration" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{entID}","privateKey":"{entKey}","customInput":{"name":"Evidence","spaceID":"{secret}","labels":["infra"]}}' through A
    Then it answers 201
    And the answer's id names "hidden"
    When token "h" sends "GET /api/v1/github-enterprise-integrations/{hidden}" through A
    Then it answers 200
    And the answer's name is '"Evidence"'
    When token "h" sends "DELETE /api/v1/github-enterprise-integrations/{hidden}" through B
    Then it answers 200
    When token "h" sends "DELETE /api/v1/github-enterprise-integrations/{hidden}" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/github-enterprise-integrations/{hidden}" through B
    Then it answers a 404 problem "not-found"

  @op:githubEnterpriseIntegration @op:vcsIntegrationTest @case:denial
  Scenario: A GitHub App integration in an unreadable space is hidden from reads and connection tests
    Given the member key is space-reader on the space "{team}"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Secret","description":"","inheritEntities":false,"parentSpace":"acme"}' through B
    And the answer's id is the space "secret"
    And token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{entID}","privateKey":"{entKey}","customInput":{"name":"Evidence","spaceID":"{secret}","labels":["infra"]}}' through A
    And it answers 201
    And the answer's id names "hidden"
    When token "m" sends "GET /api/v1/github-enterprise-integrations/{hidden}" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/vcs-integrations/{hidden}/test?provider=GITHUB_ENTERPRISE" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/github-enterprise-integrations/{hidden}" through B
    Then it answers 200
    And the answer's name is '"Evidence"'

  @op:githubEnterpriseIntegrationPatch @op:vcsIntegrationTest @case:success
  Scenario: A GitHub App merge patch changes one member, preserves the rest and returns a new ETag
    Given token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{entID}","privateKey":"{entKey}","customInput":{"name":"Evidence","spaceID":"{team}","labels":["infra"],"useGitCheckout":true,"vcsChecks":"ALL","vcsCheckOptions":{"individualFailuresOnly":true,"individualSkipUnchanged":true}},"webhookSecret":"preserved-hook-532"}' through A
    And it answers 201
    And the answer's id names "probe"
    And token "h" sends "GET /api/v1/github-enterprise-integrations/{probe}" through B
    And it answers 200
    And the answer's ETag is kept as "before"
    When token "h" sends "PATCH /api/v1/github-enterprise-integrations/{probe}" with '{"customInput":{"description":"Patched description"}}' if matching the ETag "before" through A
    Then it answers 200
    And the answer's ETag differs from "before"
    And the answer's ETag is kept as "after"
    And the answer's description is '"Patched description"'
    And the answer's name is '"Evidence"'
    And the answer's labels is '["infra"]'
    And the answer's vcsChecks is '"ALL"'
    And the answer's vcsCheckOptions is '{"individualFailuresOnly":true,"individualSkipUnchanged":true}'
    And the answer's useGitCheckout is 'true'
    And the answer's appID is '"{entID}"'
    When token "h" sends "GET /api/v1/github-enterprise-integrations/{probe}" through B
    Then it answers 200
    And the answer's description is '"Patched description"'
    And the answer's labels is '["infra"]'
    When token "h" sends "GET /api/v1/vcs-integrations/{probe}/test?provider=GITHUB_ENTERPRISE" through B
    Then it answers 200
    And the answer's status is '"SUCCESS"'
    When token "h" sends "PATCH /api/v1/github-enterprise-integrations/{probe}" with '{"privateKey":null,"customInput":{"description":null,"labels":null,"vcsChecks":null,"useGitCheckout":null,"vcsCheckOptions":null},"webhookSecret":null}' if matching the ETag "after" through A
    Then it answers 200
    And the answer's ETag differs from "after"
    And the answer's description is '""'
    And the answer's labels is '[]'
    And the answer's vcsChecks is '"INDIVIDUAL"'
    And the answer's vcsCheckOptions is '{"individualFailuresOnly":false,"individualSkipUnchanged":false}'
    And the answer's useGitCheckout is 'false'
    And the answer's webhookSecret is '"preserved-hook-532"'
    When token "h" sends "GET /api/v1/vcs-integrations/{probe}/test?provider=GITHUB_ENTERPRISE" through B
    Then it answers 200
    And the answer's status is '"SUCCESS"'

  @op:githubEnterpriseIntegrationPatch @case:denial
  Scenario: A GitHub App reader cannot merge patch an integration it can read
    Given the member key is space-reader on the space "{team}"
    And token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{entID}","privateKey":"{entKey}","customInput":{"name":"Evidence","spaceID":"{team}","labels":["infra"]}}' through A
    And it answers 201
    And the answer's id names "probe"
    When token "m" sends "GET /api/v1/github-enterprise-integrations/{probe}" through B
    Then it answers 200
    When token "m" sends "PATCH /api/v1/github-enterprise-integrations/{probe}" with '{"customInput":{"description":"Forbidden description"}}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_ADMIN"'
    When token "h" sends "GET /api/v1/github-enterprise-integrations/{probe}" through B
    Then it answers 200
    And the answer's description is '""'
    And the answer's labels is '["infra"]'

  @op:githubEnterpriseIntegrationPatch @case:failure
  Scenario: A GitHub App merge patch refuses a stale ETag and unknown members without changing the integration
    Given token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{entID}","privateKey":"{entKey}","customInput":{"name":"Evidence","spaceID":"{team}","labels":["infra"]}}' through A
    And it answers 201
    And the answer's id names "probe"
    And token "h" sends "GET /api/v1/github-enterprise-integrations/{probe}" through B
    And it answers 200
    And the answer's ETag is kept as "before"
    When token "h" sends "PATCH /api/v1/github-enterprise-integrations/{probe}" with '{"customInput":{"description":"Current description"}}' if matching the ETag "before" through A
    Then it answers 200
    And the answer's ETag differs from "before"
    And the answer's ETag is kept as "current"
    When token "h" sends "PATCH /api/v1/github-enterprise-integrations/{probe}" with '{"customInput":{"description":"Stale description"}}' if matching the ETag "before" through B
    Then it answers a 412 problem "precondition-failed"
    When token "h" sends "PATCH /api/v1/github-enterprise-integrations/{probe}" with '{"unrecognizedSetting":true}' if matching the ETag "current" through A
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "PATCH /api/v1/github-enterprise-integrations/{probe}" with '{"customInput":{"unrecognizedSetting":true}}' if matching the ETag "current" through B
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "GET /api/v1/github-enterprise-integrations/{probe}" through A
    Then it answers 200
    And the answer's description is '"Current description"'
    And the answer's labels is '["infra"]'
    When token "h" sends "PATCH /api/v1/github-enterprise-integrations/{probe}" with '{"customInput":{"description":"Final description"}}' if matching the ETag "current" through B
    Then it answers 200
    And the answer's description is '"Final description"'

  @op:githubEnterpriseIntegrationRegenerateWebhookSecret @op:githubEnterpriseWebhooksEndpoint @case:success @webhook-controls
  Scenario: A GitHub App admin rotates secrets once per key and reads the configured delivery URL
    When token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{entID}","privateKey":"{entKey}","customInput":{"name":"Webhook control","spaceID":"acme","isDefault":true}}' through A
    Then it answers 201
    And the answer's id names "hookIntegration"
    And the answer's webhook secret is kept as "originalHook"
    When token "h" sends "GET /api/v1/github-enterprise-webhooks-endpoint" through B
    Then it answers 200
    And the answer is '"http://infraweave.test/webhooks/vcs/github_enterprise/{hookIntegration}"'
    When token "h" regenerates the webhook secret at "/api/v1/github-enterprise-integrations/{hookIntegration}/regenerate-webhook-secret" with idempotency key "webhook-control-first" through A
    Then it answers 200
    And the answer's webhook secret differs from "originalHook"
    And the answer's webhook secret is kept as "firstHook"
    When token "h" sends "GET /api/v1/github-enterprise-integrations/{hookIntegration}" through B
    Then it answers 200
    And the answer's webhookSecret is '"{firstHook}"'
    When token "h" regenerates the webhook secret at "/api/v1/github-enterprise-integrations/{hookIntegration}/regenerate-webhook-secret" with idempotency key "webhook-control-first" through B
    Then it answers 200
    And the answer's webhookSecret is '"{firstHook}"'
    And integration "hookIntegration" has 1 secret rotation audit entries and never logs or audits secrets "originalHook, firstHook"
    When token "h" sends "POST /api/v1/github-enterprise-integrations/{hookIntegration}/regenerate-webhook-secret" through B
    Then it answers 200
    And the answer's webhook secret differs from "firstHook"
    And the answer's webhook secret is kept as "secondHook"
    When token "h" sends "GET /api/v1/github-enterprise-integrations/{hookIntegration}" through A
    Then it answers 200
    And the answer's webhookSecret is '"{secondHook}"'
    And integration "hookIntegration" has 2 secret rotation audit entries and never logs or audits secrets "originalHook, firstHook, secondHook"
    When token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{entID}","privateKey":"{entKey}","customInput":{"name":"Named webhook App","spaceID":"{team}"}}' through B
    Then it answers 201
    And the answer's id names "namedHookApp"
    When token "h" sends "GET /api/v1/github-enterprise-webhooks-endpoint?id={namedHookApp}" through A
    Then it answers 200
    And the answer is '"http://infraweave.test/webhooks/vcs/github_enterprise/{namedHookApp}"'
    When token "h" sends "GET /api/v1/github-enterprise-webhooks-endpoint" through B
    Then it answers 200
    And the answer is '"http://infraweave.test/webhooks/vcs/github_enterprise/{hookIntegration}"'

  @op:githubEnterpriseIntegrationRegenerateWebhookSecret @op:githubEnterpriseWebhooksEndpoint @case:denial @webhook-controls
  Scenario: A GitHub App space reader cannot rotate a secret or read the delivery URL
    Given the member key is space-reader on the space "acme"
    And token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{entID}","privateKey":"{entKey}","customInput":{"name":"Protected webhook","spaceID":"acme","isDefault":true}}' through A
    And it answers 201
    And the answer's id names "hookIntegration"
    And the answer's webhook secret is kept as "untouchedHook"
    When token "m" sends "GET /api/v1/github-enterprise-webhooks-endpoint" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_ADMIN"'
    When token "m" sends "POST /api/v1/github-enterprise-integrations/{hookIntegration}/regenerate-webhook-secret" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_ADMIN"'
    When token "h" sends "GET /api/v1/github-enterprise-integrations/{hookIntegration}" through B
    Then it answers 200
    And the answer's webhookSecret is '"{untouchedHook}"'
    And integration "hookIntegration" has 0 secret rotation audit entries and never logs or audits secrets "untouchedHook"

  @op:githubEnterpriseIntegrationRegenerateWebhookSecret @op:githubEnterpriseWebhooksEndpoint @case:failure @webhook-controls
  Scenario: GitHub App webhook controls refuse missing defaults and missing or hidden integrations
    Given the member key is space-reader on the space "{team}"
    When token "h" sends "GET /api/v1/github-enterprise-webhooks-endpoint" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/github-enterprise-integrations/missing-integration/regenerate-webhook-secret" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/spaces" with '{"name":"Private hooks","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    Then it answers 201
    And the answer's id is the space "privateHooks"
    When token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{entID}","privateKey":"{entKey}","customInput":{"name":"Hidden hook","spaceID":"{privateHooks}"}}' through B
    Then it answers 201
    And the answer's id names "hookIntegration"
    When token "m" sends "POST /api/v1/github-enterprise-integrations/{hookIntegration}/regenerate-webhook-secret" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/github-enterprise-webhooks-endpoint?id={hookIntegration}" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/github-enterprise-webhooks-endpoint?id=missing-integration" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "DELETE /api/v1/github-enterprise-integrations/{hookIntegration}" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/github-enterprise-integrations/{hookIntegration}/regenerate-webhook-secret" through A
    Then it answers a 404 problem "not-found"

  @op:githubEnterpriseWebhooksEndpoint @case:success @webhook-controls
  Scenario: The named App endpoint uses that integration's space rather than the default's top space
    Given the member key is space-admin on the space "{team}"
    And token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{entID}","privateKey":"{entKey}","customInput":{"name":"Top webhook App","spaceID":"acme","isDefault":true}}' through A
    And it answers 201
    And token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{entID}","privateKey":"{entKey}","customInput":{"name":"Team webhook App","spaceID":"{team}"}}' through B
    And it answers 201
    And the answer's id names "teamHookApp"
    When token "m" sends "GET /api/v1/github-enterprise-webhooks-endpoint?id={teamHookApp}" through A
    Then it answers 200
    And the answer is '"http://infraweave.test/webhooks/vcs/github_enterprise/{teamHookApp}"'
    When token "m" sends "GET /api/v1/github-enterprise-webhooks-endpoint" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_ADMIN"'

  @op:vcsIntegrationTest @case:failure @webhook-controls
  Scenario: The GitHub App check names a missing consumed event and a missing write permission
    Given token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{entID}","privateKey":"{entKey}","customInput":{"name":"App event checks","spaceID":"{team}"}}' through A
    And it answers 201
    And the answer's id names "checkedApp"
    When the GitHub App "ent" lacks required event "check_run"
    And token "h" sends "GET /api/v1/vcs-integrations/{checkedApp}/test?provider=GITHUB_ENTERPRISE" through B
    Then it answers 200
    And the answer's GITHUB_APP_EVENTS check is FAIL
    And the failed GitHub App check names missing event "check_run"
    When the GitHub App "ent" lacks required permission "deployments"
    And token "h" sends "GET /api/v1/vcs-integrations/{checkedApp}/test?provider=GITHUB_ENTERPRISE" through A
    Then it answers 200
    And the answer's GITHUB_APP_EVENTS check is FAIL
    And the failed GitHub App check names missing permission "deployments"

  @op:githubAppCreateFromManifest @op:vcsIntegrationTest @case:success @webhook-controls
  Scenario: The manifest-created App requests every permission and subscribed event its check needs
    When token "h" sends "POST /api/v1/github-app-manifests" with '{"ownerType":"ORG","ownerName":"acme","host":"{githubHost}","apiHost":"{githubHost}"}' through A
    Then it answers 200
    And the answer's manifest state is kept as "hookManifestState"
    And the GitHub host hands back a manifest code kept as "hookManifestCode"
    When token "h" sends "POST /api/v1/github-enterprise-integrations/from-manifest" with '{"code":"{hookManifestCode}","state":"{hookManifestState}"}' through B
    Then it answers 201
    And the answer's id names "manifestHookApp"
    When token "h" sends "GET /api/v1/vcs-integrations/{manifestHookApp}/test?provider=GITHUB_ENTERPRISE" through A
    Then it answers 200
    And the answer's GITHUB_APP_EVENTS check is SUCCESS
    And the answer's status is '"SUCCESS"'
