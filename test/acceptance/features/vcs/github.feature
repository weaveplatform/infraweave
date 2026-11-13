@postgres @github
Feature: GitHub integrations
  An account reaches GitHub through the operator's GitHub App, whose check
  options it sets, or through GitHub Apps of its own on github.com or a
  GitHub Enterprise Server, which it registers by their keys or creates
  through a manifest form. The scenarios' GitHub host is an in-process
  fake at {githubHost}, a GitHub Enterprise Server to the servers, with the
  operator's App installed on acme. A run of a stack on a private
  repository fetches its source by Git with an installation token minted
  for that repository alone, which only the specification's sealed part
  carries; the release indexes serve the recorded fixtures, so the run's
  specification names a Terraform release. The home key is space-admin on
  the account's top space; the member key holds only the bindings a
  scenario gives it. Space team inherits from the top space.

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And a GitHub App "ent" installed on the organization "acme"

  @op:githubIntegration @op:githubIntegrationUpdate @case:success
  Scenario: An admin reads and sets the check options of the operator's App
    When token "h" sends "GET /api/v1/github-integration" through A
    Then it answers 200
    And the answer's apiHost is '"{githubHost}"'
    And the answer's isEnterprise is 'true'
    And the answer's vcsChecks is '"INDIVIDUAL"'
    When token "h" sends "PUT /api/v1/github-integration" with '{"vcsChecks":"ALL","useGitCheckout":true,"vcsCheckOptions":{"individualFailuresOnly":true}}' through B
    Then it answers 200
    And the answer's vcsChecks is '"ALL"'
    When token "h" sends "PUT /api/v1/github-integration" with '{"vcsChecks":"AGGREGATED"}' through A
    Then it answers 200
    When token "h" sends "GET /api/v1/github-integration" through B
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

  @op:githubEnterpriseIntegrationCreate @op:githubEnterpriseIntegration @op:githubEnterpriseIntegrationUpdate @op:githubEnterpriseIntegrationDelete @op:githubEnterpriseIntegrationTest @op:githubIntegrationUpdate @op:githubAppGenerateManifest @op:githubAppCreateFromManifest @case:denial
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
    When token "m" sends "PUT /api/v1/github-integration" with '{"vcsChecks":"ALL"}' through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/github-app-manifests" with '{"ownerType":"USER"}' through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/github-enterprise-integrations/from-manifest" with '{"code":"x","state":"y"}' through A
    Then it answers a 403 problem "forbidden"

  @op:githubEnterpriseIntegrationCreate @op:githubEnterpriseIntegrationTest @op:githubEnterpriseIntegrationUpdate @op:githubAppGenerateManifest @op:githubAppCreateFromManifest @op:githubIntegrationUpdate @case:failure
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
    When token "h" sends "PUT /api/v1/github-integration" with '{"vcsChecks":"SOME"}' through A
    Then it answers a 400 problem "invalid-request"

  @op:repositories @op:branches @case:success
  Scenario: A user pages the repositories and branches of the operator's App
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
  Scenario: A stack on a private repository tracks its branch through the operator's App, and pins an earlier commit
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
