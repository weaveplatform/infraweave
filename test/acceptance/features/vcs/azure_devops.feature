@postgres @azure-devops
Feature: Azure DevOps integrations
  An account reaches an Azure DevOps organization with a personal access
  token, which it never reads back, limited to the projects it names or
  reaching all of them. A stack on Azure DevOps names its repository by
  its project and its name, and reads it through the integration it names
  or its provider's default. The scenarios' organization is an in-process
  fake at {azureDevOpsOrg}. The home key is space-admin on the account's
  top space; the member key holds only the bindings a scenario gives it.
  Space team inherits from the top space.

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And an Azure DevOps user "dev" with a personal access token kept as "tok"
    And an Azure DevOps repository "Platform/infra" that "dev" administers, its head of "main" kept as "seed"

  @op:azureDevOpsRepoIntegrationCreate @op:azureDevOpsRepoIntegration @op:azureDevOpsRepoIntegrationUpdate @op:azureDevOpsRepoIntegrationTest @op:azureDevOpsRepoIntegrationDelete @op:vcsIntegration @case:success
  Scenario: An admin connects Azure DevOps by a token, tests, reads, updates and deletes it
    When token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations" with '{"organizationURL":"{azureDevOpsOrg}","personalAccessToken":"{tok}","accessibleProjects":["Platform"],"customInput":{"name":"Azure","spaceID":"{team}","labels":["infra"]}}' through A
    Then it answers 201
    And the answer's id names "d"
    And the answer's accessibleProjects is '["Platform"]'
    And the answer's webhookUrl is '"http://infraweave.test/webhooks/vcs/azure_devops/{d}"'
    When token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations/{d}/test" through B
    Then it answers 200
    And the answer is 'true'
    When token "h" sends "GET /api/v1/azure-dev-ops-repo-integrations/{d}" through B
    Then it answers 200
    And the answer's name is '"Azure"'
    And the answer's labels is '["infra"]'
    And the answer leaves out "personalAccessToken"
    When token "h" sends "GET /api/v1/vcs-integrations/{d}?provider=AZURE_DEVOPS" through A
    Then it answers 200
    When token "h" sends "PUT /api/v1/azure-dev-ops-repo-integrations/{d}" with '{"organizationURL":"{azureDevOpsOrg}","userFacingHost":"https://dev.azure.example/acme","customInput":{"space":"{team}","description":"The team organization","vcsChecks":"AGGREGATED"}}' through A
    Then it answers 200
    And the answer's ETag is kept as "updated"
    And the answer's description is '"The team organization"'
    And the answer's userFacingHost is '"https://dev.azure.example/acme"'
    And the answer's accessibleProjects is '["Platform"]'
    And the answer's vcsChecks is '"AGGREGATED"'
    When token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations/{d}/test" through B
    Then the answer is 'true'
    When token "h" sends "DELETE /api/v1/azure-dev-ops-repo-integrations/{d}" through B
    Then it answers 200
    When token "h" sends "GET /api/v1/azure-dev-ops-repo-integrations/{d}" through A
    Then it answers a 404 problem "not-found"

  @op:azureDevOpsRepoIntegrationCreate @op:stackCreate @op:stackSyncCommit @op:stackSetCurrentCommit @case:success
  Scenario: A stack on a project's repository tracks its branch through its provider's default, and pins an earlier commit
    When token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations" with '{"organizationURL":"{azureDevOpsOrg}","personalAccessToken":"{tok}","customInput":{"name":"Main","spaceID":"acme","isDefault":true}}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","namespace":"Platform","repository":"infra","branch":"main","provider":"AZURE_DEVOPS","space":"{team}"},"manageState":false}' through B
    Then it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    When a commit to "main" of the Azure DevOps repository "Platform/infra" is kept as "pushed"
    And token "h" sends "POST /api/v1/stacks/infra/sync-commit" through B
    Then it answers 200
    And the answer tracks the commit kept as "pushed" at the branch head kept as "pushed"
    When token "h" sends "POST /api/v1/stacks/infra/set-current-commit" with '{"sha":"{seed}"}' through A
    Then it answers 200
    And the answer tracks the commit kept as "seed" at the branch head kept as "pushed"

  @op:azureDevOpsRepoIntegrationCreate @op:azureDevOpsRepoIntegration @op:azureDevOpsRepoIntegrationUpdate @op:azureDevOpsRepoIntegrationDelete @op:azureDevOpsRepoIntegrationTest @case:denial
  Scenario: A reader of a space sees its integrations without their secrets, and changes nothing
    Given the member key is space-reader on the space "{team}"
    When token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations" with '{"organizationURL":"{azureDevOpsOrg}","personalAccessToken":"{tok}","customInput":{"name":"Azure","spaceID":"{team}"}}' through A
    Then the answer's id names "d"
    When token "m" sends "GET /api/v1/azure-dev-ops-repo-integrations/{d}" through B
    Then it answers 200
    And the answer's webhookPassword is '""'
    And the answer's webhookUrl is '""'
    When token "m" sends "POST /api/v1/azure-dev-ops-repo-integrations" with '{"organizationURL":"{azureDevOpsOrg}","personalAccessToken":"{tok}","customInput":{"name":"Mine","spaceID":"{team}"}}' through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PUT /api/v1/azure-dev-ops-repo-integrations/{d}" with '{"organizationURL":"{azureDevOpsOrg}","customInput":{"space":"{team}"}}' through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "DELETE /api/v1/azure-dev-ops-repo-integrations/{d}" through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/azure-dev-ops-repo-integrations/{d}/test" through B
    Then it answers a 403 problem "forbidden"

  @op:azureDevOpsRepoIntegrationCreate @op:azureDevOpsRepoIntegrationTest @op:azureDevOpsRepoIntegrationUpdate @op:stackSyncCommit @case:failure
  Scenario: A bad organization URL, a refused token, another space and a stack with no integration are refused
    When token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations" with '{"organizationURL":"http://dev.azure.example/acme","personalAccessToken":"t","customInput":{"name":"Bad","spaceID":"acme"}}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/organizationURL"
    When token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations" with '{"organizationURL":"{azureDevOpsOrg}","personalAccessToken":"revoked","customInput":{"name":"Revoked","spaceID":"acme"}}' through B
    Then it answers 201
    And the answer's id names "r"
    When token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations/{r}/test" through A
    Then it answers 200
    And the answer is 'false'
    When token "h" sends "PUT /api/v1/azure-dev-ops-repo-integrations/{r}" with '{"organizationURL":"{azureDevOpsOrg}","customInput":{"space":"{team}"}}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/customInput/space"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Orphan","namespace":"Platform","repository":"infra","branch":"main","provider":"AZURE_DEVOPS","space":"{team}"},"manageState":false}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/stacks/orphan/sync-commit" through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/vcsIntegrationId"

  @op:azureDevOpsRepoIntegrationCreate @op:repositories @op:branches @case:success
  Scenario: A user pages the repositories and branches of the provider's default
    Given an Azure DevOps repository "Platform/web" that "dev" administers, its head of "main" kept as "web"
    And a commit to "dev" of the Azure DevOps repository "Platform/infra" is kept as "dev"
    When token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations" with '{"organizationURL":"{azureDevOpsOrg}","personalAccessToken":"{tok}","customInput":{"name":"Main","spaceID":"acme","isDefault":true}}' through A
    Then it answers 201
    When token "h" sends "GET /api/v1/repositories?provider=AZURE_DEVOPS&limit=1" through B
    Then it answers 200
    And the answer's items is '[{"namespace":"Platform","id":"infra"}]'
    And the answer's nextCursor is kept as "next"
    When token "h" sends "GET /api/v1/repositories?provider=AZURE_DEVOPS&limit=1&cursor={next}" through A
    Then it answers 200
    And the answer's items is '[{"namespace":"Platform","id":"web"}]'
    And the answer leaves out "nextCursor"
    When token "h" sends "GET /api/v1/branches?provider=AZURE_DEVOPS&namespace=Platform&repository=infra&limit=1" through B
    Then it answers 200
    And the answer's items is '["dev"]'
    And the answer's nextCursor is kept as "next"
    When token "h" sends "GET /api/v1/branches?provider=AZURE_DEVOPS&namespace=Platform&repository=infra&limit=1&cursor={next}" through A
    Then it answers 200
    And the answer's items is '["main"]'
    And the answer leaves out "nextCursor"

  @op:azureDevOpsRepoIntegration @op:azureDevOpsRepoIntegrationDelete @case:failure
  Scenario: A missing Azure DevOps integration cannot be read or deleted, and deletion succeeds only once
    Given the member key is space-reader on the space "{team}"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Secret","description":"","inheritEntities":false,"parentSpace":"acme"}' through B
    And the answer's id is the space "secret"
    When token "h" sends "GET /api/v1/azure-dev-ops-repo-integrations/missing-integration" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "DELETE /api/v1/azure-dev-ops-repo-integrations/missing-integration" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations" with '{"organizationURL":"{azureDevOpsOrg}","personalAccessToken":"{tok}","accessibleProjects":["Platform"],"customInput":{"name":"Evidence","spaceID":"{secret}","labels":["infra"]}}' through A
    Then it answers 201
    And the answer's id names "hidden"
    When token "h" sends "GET /api/v1/azure-dev-ops-repo-integrations/{hidden}" through A
    Then it answers 200
    And the answer's name is '"Evidence"'
    When token "h" sends "DELETE /api/v1/azure-dev-ops-repo-integrations/{hidden}" through B
    Then it answers 200
    When token "h" sends "DELETE /api/v1/azure-dev-ops-repo-integrations/{hidden}" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/azure-dev-ops-repo-integrations/{hidden}" through B
    Then it answers a 404 problem "not-found"

  @op:azureDevOpsRepoIntegration @op:vcsIntegrationTest @case:denial
  Scenario: A Azure DevOps integration in an unreadable space is hidden from reads and connection tests
    Given the member key is space-reader on the space "{team}"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Secret","description":"","inheritEntities":false,"parentSpace":"acme"}' through B
    And the answer's id is the space "secret"
    And token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations" with '{"organizationURL":"{azureDevOpsOrg}","personalAccessToken":"{tok}","accessibleProjects":["Platform"],"customInput":{"name":"Evidence","spaceID":"{secret}","labels":["infra"]}}' through A
    And it answers 201
    And the answer's id names "hidden"
    When token "m" sends "GET /api/v1/azure-dev-ops-repo-integrations/{hidden}" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/vcs-integrations/{hidden}/test?provider=AZURE_DEVOPS" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/azure-dev-ops-repo-integrations/{hidden}" through B
    Then it answers 200
    And the answer's name is '"Evidence"'

  @op:azureDevOpsRepoIntegrationPatch @op:vcsIntegrationTest @case:success
  Scenario: A Azure DevOps merge patch changes one member, preserves the rest and returns a new ETag
    Given token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations" with '{"organizationURL":"{azureDevOpsOrg}","personalAccessToken":"{tok}","accessibleProjects":["Platform"],"customInput":{"name":"Evidence","spaceID":"{team}","labels":["infra"],"useGitCheckout":true,"vcsChecks":"ALL","vcsCheckOptions":{"individualFailuresOnly":true,"individualSkipUnchanged":true}}}' through A
    And it answers 201
    And the answer's id names "probe"
    And token "h" sends "GET /api/v1/azure-dev-ops-repo-integrations/{probe}" through B
    And it answers 200
    And the answer's ETag is kept as "before"
    When token "h" sends "PATCH /api/v1/azure-dev-ops-repo-integrations/{probe}" with '{"customInput":{"description":"Patched description"}}' if matching the ETag "before" through A
    Then it answers 200
    And the answer's ETag differs from "before"
    And the answer's ETag is kept as "after"
    And the answer's description is '"Patched description"'
    And the answer's name is '"Evidence"'
    And the answer's labels is '["infra"]'
    And the answer's vcsChecks is '"ALL"'
    And the answer's vcsCheckOptions is '{"individualFailuresOnly":true,"individualSkipUnchanged":true}'
    And the answer's useGitCheckout is 'true'
    And the answer's accessibleProjects is '["Platform"]'
    When token "h" sends "GET /api/v1/azure-dev-ops-repo-integrations/{probe}" through B
    Then it answers 200
    And the answer's description is '"Patched description"'
    And the answer's labels is '["infra"]'
    When token "h" sends "GET /api/v1/vcs-integrations/{probe}/test?provider=AZURE_DEVOPS" through B
    Then it answers 200
    And the answer's status is '"SUCCESS"'
    When token "h" sends "PATCH /api/v1/azure-dev-ops-repo-integrations/{probe}" with '{"personalAccessToken":null,"customInput":{"description":null,"labels":null,"vcsChecks":null,"useGitCheckout":null,"vcsCheckOptions":null}}' if matching the ETag "after" through A
    Then it answers 200
    And the answer's ETag differs from "after"
    And the answer's description is '""'
    And the answer's labels is '[]'
    And the answer's vcsChecks is '"INDIVIDUAL"'
    And the answer's vcsCheckOptions is '{"individualFailuresOnly":false,"individualSkipUnchanged":false}'
    And the answer's useGitCheckout is 'false'
    When token "h" sends "GET /api/v1/vcs-integrations/{probe}/test?provider=AZURE_DEVOPS" through B
    Then it answers 200
    And the answer's status is '"SUCCESS"'

  @op:azureDevOpsRepoIntegrationPatch @case:denial
  Scenario: A Azure DevOps reader cannot merge patch an integration it can read
    Given the member key is space-reader on the space "{team}"
    And token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations" with '{"organizationURL":"{azureDevOpsOrg}","personalAccessToken":"{tok}","accessibleProjects":["Platform"],"customInput":{"name":"Evidence","spaceID":"{team}","labels":["infra"]}}' through A
    And it answers 201
    And the answer's id names "probe"
    When token "m" sends "GET /api/v1/azure-dev-ops-repo-integrations/{probe}" through B
    Then it answers 200
    When token "m" sends "PATCH /api/v1/azure-dev-ops-repo-integrations/{probe}" with '{"customInput":{"description":"Forbidden description"}}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_ADMIN"'
    When token "h" sends "GET /api/v1/azure-dev-ops-repo-integrations/{probe}" through B
    Then it answers 200
    And the answer's description is '""'
    And the answer's labels is '["infra"]'

  @op:azureDevOpsRepoIntegrationPatch @case:failure
  Scenario: A Azure DevOps merge patch refuses a stale ETag and unknown members without changing the integration
    Given token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations" with '{"organizationURL":"{azureDevOpsOrg}","personalAccessToken":"{tok}","accessibleProjects":["Platform"],"customInput":{"name":"Evidence","spaceID":"{team}","labels":["infra"]}}' through A
    And it answers 201
    And the answer's id names "probe"
    And token "h" sends "GET /api/v1/azure-dev-ops-repo-integrations/{probe}" through B
    And it answers 200
    And the answer's ETag is kept as "before"
    When token "h" sends "PATCH /api/v1/azure-dev-ops-repo-integrations/{probe}" with '{"customInput":{"description":"Current description"}}' if matching the ETag "before" through A
    Then it answers 200
    And the answer's ETag differs from "before"
    And the answer's ETag is kept as "current"
    When token "h" sends "PATCH /api/v1/azure-dev-ops-repo-integrations/{probe}" with '{"customInput":{"description":"Stale description"}}' if matching the ETag "before" through B
    Then it answers a 412 problem "precondition-failed"
    When token "h" sends "PATCH /api/v1/azure-dev-ops-repo-integrations/{probe}" with '{"unrecognizedSetting":true}' if matching the ETag "current" through A
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "PATCH /api/v1/azure-dev-ops-repo-integrations/{probe}" with '{"customInput":{"unrecognizedSetting":true}}' if matching the ETag "current" through B
    Then it answers a 400 problem "invalid-request"
    When token "h" sends "GET /api/v1/azure-dev-ops-repo-integrations/{probe}" through A
    Then it answers 200
    And the answer's description is '"Current description"'
    And the answer's labels is '["infra"]'
    When token "h" sends "PATCH /api/v1/azure-dev-ops-repo-integrations/{probe}" with '{"customInput":{"description":"Final description"}}' if matching the ETag "current" through B
    Then it answers 200
    And the answer's description is '"Final description"'

  @op:azureDevOpsRepoIntegrationRegenerateWebhookSecret @op:azureDevOpsWebhooksEndpoint @case:success @webhook-controls
  Scenario: An Azure DevOps admin rotates secrets once per key and reads the configured delivery URL
    When token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations" with '{"organizationURL":"{azureDevOpsOrg}","personalAccessToken":"{tok}","accessibleProjects":["Platform"],"customInput":{"name":"Webhook control","spaceID":"acme","isDefault":true}}' through A
    Then it answers 201
    And the answer's id names "hookIntegration"
    And the answer's webhook secret is kept as "originalHook"
    When token "h" sends "GET /api/v1/azure-dev-ops-webhooks-endpoint" through B
    Then it answers 200
    And the answer is '"http://infraweave.test/webhooks/vcs/azure_devops/{hookIntegration}"'
    When token "h" regenerates the webhook secret at "/api/v1/azure-dev-ops-repo-integrations/{hookIntegration}/regenerate-webhook-secret" with idempotency key "webhook-control-first" through A
    Then it answers 200
    And the answer's webhook secret differs from "originalHook"
    And the answer's webhook secret is kept as "firstHook"
    When token "h" sends "GET /api/v1/azure-dev-ops-repo-integrations/{hookIntegration}" through B
    Then it answers 200
    And the answer's webhookSecret is '"{firstHook}"'
    When token "h" regenerates the webhook secret at "/api/v1/azure-dev-ops-repo-integrations/{hookIntegration}/regenerate-webhook-secret" with idempotency key "webhook-control-first" through B
    Then it answers 200
    And the answer's webhookSecret is '"{firstHook}"'
    And integration "hookIntegration" has 1 secret rotation audit entries and never logs or audits secrets "originalHook, firstHook"
    When token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations/{hookIntegration}/regenerate-webhook-secret" through B
    Then it answers 200
    And the answer's webhook secret differs from "firstHook"
    And the answer's webhook secret is kept as "secondHook"
    When token "h" sends "GET /api/v1/azure-dev-ops-repo-integrations/{hookIntegration}" through A
    Then it answers 200
    And the answer's webhookSecret is '"{secondHook}"'
    And integration "hookIntegration" has 2 secret rotation audit entries and never logs or audits secrets "originalHook, firstHook, secondHook"

  @op:azureDevOpsRepoIntegrationRegenerateWebhookSecret @op:azureDevOpsWebhooksEndpoint @case:denial @webhook-controls
  Scenario: An Azure DevOps space reader cannot rotate a secret or read the delivery URL
    Given the member key is space-reader on the space "acme"
    And token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations" with '{"organizationURL":"{azureDevOpsOrg}","personalAccessToken":"{tok}","accessibleProjects":["Platform"],"customInput":{"name":"Protected webhook","spaceID":"acme","isDefault":true}}' through A
    And it answers 201
    And the answer's id names "hookIntegration"
    And the answer's webhook secret is kept as "untouchedHook"
    When token "m" sends "GET /api/v1/azure-dev-ops-webhooks-endpoint" through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_ADMIN"'
    When token "m" sends "POST /api/v1/azure-dev-ops-repo-integrations/{hookIntegration}/regenerate-webhook-secret" through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"SPACE_ADMIN"'
    When token "h" sends "GET /api/v1/azure-dev-ops-repo-integrations/{hookIntegration}" through B
    Then it answers 200
    And the answer's webhookSecret is '"{untouchedHook}"'
    And integration "hookIntegration" has 0 secret rotation audit entries and never logs or audits secrets "untouchedHook"

  @op:azureDevOpsRepoIntegrationRegenerateWebhookSecret @op:azureDevOpsWebhooksEndpoint @case:failure @webhook-controls
  Scenario: Azure DevOps webhook controls refuse missing defaults and missing or hidden integrations
    Given the member key is space-reader on the space "{team}"
    When token "h" sends "GET /api/v1/azure-dev-ops-webhooks-endpoint" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations/missing-integration/regenerate-webhook-secret" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/spaces" with '{"name":"Private hooks","description":"","inheritEntities":false,"parentSpace":"acme"}' through A
    Then it answers 201
    And the answer's id is the space "privateHooks"
    When token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations" with '{"organizationURL":"{azureDevOpsOrg}","personalAccessToken":"{tok}","accessibleProjects":["Platform"],"customInput":{"name":"Hidden hook","spaceID":"{privateHooks}"}}' through B
    Then it answers 201
    And the answer's id names "hookIntegration"
    When token "m" sends "POST /api/v1/azure-dev-ops-repo-integrations/{hookIntegration}/regenerate-webhook-secret" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "DELETE /api/v1/azure-dev-ops-repo-integrations/{hookIntegration}" through B
    Then it answers 200
    When token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations/{hookIntegration}/regenerate-webhook-secret" through A
    Then it answers a 404 problem "not-found"

  @op:vcsIntegrationTest @case:failure @webhook-controls
  Scenario: An Azure DevOps connection test checks the delivery URL and every consumed hook event
    Given token "h" sends "POST /api/v1/azure-dev-ops-repo-integrations" with '{"organizationURL":"{azureDevOpsOrg}","personalAccessToken":"{tok}","accessibleProjects":["Platform"],"customInput":{"name":"Hook check","spaceID":"acme","isDefault":true}}' through A
    And it answers 201
    And the answer's id names "hookIntegration"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Hook source","namespace":"Platform","repository":"infra","branch":"main","provider":"AZURE_DEVOPS","vcsIntegrationId":"{hookIntegration}","space":"{team}"},"manageState":false}' through B
    And it answers 201
    And the Azure DevOps repository "Platform/infra" has no webhook
    When token "h" sends "GET /api/v1/vcs-integrations/{hookIntegration}/test?provider=AZURE_DEVOPS" through A
    Then it answers 200
    And the answer's WEBHOOK check is FAIL
    And the failed webhook check explains "Platform/infra" against "http://infraweave.test/webhooks/vcs/azure_devops/{hookIntegration}"
    When the Azure DevOps repository "Platform/infra" has a webhook to "https://elsewhere.invalid/hooks" for events "git.push, git.pullrequest.created, git.pullrequest.updated, git.pullrequest.merged, ms.vss-code.git-pullrequest-comment-event"
    And token "h" sends "GET /api/v1/vcs-integrations/{hookIntegration}/test?provider=AZURE_DEVOPS" through B
    Then it answers 200
    And the answer's WEBHOOK check is FAIL
    And the failed webhook check explains "Platform/infra" against "http://infraweave.test/webhooks/vcs/azure_devops/{hookIntegration}"
    When the Azure DevOps repository "Platform/infra" has a webhook to "http://infraweave.test/webhooks/vcs/azure_devops/{hookIntegration}" for events "git.push"
    And token "h" sends "GET /api/v1/vcs-integrations/{hookIntegration}/test?provider=AZURE_DEVOPS" through A
    Then it answers 200
    And the answer's WEBHOOK check is FAIL
    And the failed webhook check explains "Platform/infra" against "http://infraweave.test/webhooks/vcs/azure_devops/{hookIntegration}"
    When the Azure DevOps repository "Platform/infra" has a webhook to "http://infraweave.test/webhooks/vcs/azure_devops/{hookIntegration}" for events "git.push, git.pullrequest.created, git.pullrequest.updated, git.pullrequest.merged, ms.vss-code.git-pullrequest-comment-event"
    And token "h" sends "GET /api/v1/vcs-integrations/{hookIntegration}/test?provider=AZURE_DEVOPS" through B
    Then it answers 200
    And the answer's WEBHOOK check is SUCCESS
    And the answer's status is '"SUCCESS"'
