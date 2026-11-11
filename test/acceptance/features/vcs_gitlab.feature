@postgres @gitlab
Feature: GitLab integrations
  An account reaches GitLab.com or a self-managed GitLab with an access
  token, which it never reads back. A stack on GitLab names its project by
  its namespace, a group path with any subgroups, and its name, and reads
  it through the integration it names or its provider's default. The
  scenarios' GitLab is an in-process fake at {gitlabHost}. The home key is
  space-admin on the account's top space; the member key holds only the
  bindings a scenario gives it. Space team inherits from the top space.

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And a GitLab user "dev" with a token kept as "tok"
    And a GitLab project "acme/platform/infra" that "dev" maintains, its head of "main" kept as "seed"

  @op:gitlabIntegrationCreate @op:gitlabIntegration @op:gitlabIntegrationUpdate @op:gitlabIntegrationTest @op:gitlabIntegrationDelete @op:vcsIntegration @case:success
  Scenario: An admin connects GitLab by a token, tests, reads, updates and deletes it
    When token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"{tok}","customInput":{"name":"Lab","spaceID":"{team}","labels":["infra"]}}' through A
    Then it answers 201
    And the answer's id names "g"
    And the answer's userFacingHost is '"{gitlabHost}"'
    And the answer's webhookUrl is '"http://infraweave.test/webhooks/vcs/gitlab/{g}"'
    When token "h" sends "POST /api/v1/gitlab-integrations/{g}/test" through B
    Then it answers 200
    And the answer is 'true'
    When token "h" sends "GET /api/v1/gitlab-integrations/{g}" through B
    Then it answers 200
    And the answer's name is '"Lab"'
    And the answer's labels is '["infra"]'
    And the answer leaves out "privateToken"
    When token "h" sends "GET /api/v1/vcs-integrations/{g}?provider=GITLAB" through A
    Then it answers 200
    When token "h" sends "PUT /api/v1/gitlab-integrations/{g}" with '{"userFacingHost":"https://gitlab.example","customInput":{"space":"{team}","description":"The team GitLab","vcsChecks":"AGGREGATED"}}' through A
    Then it answers 200
    And the answer's description is '"The team GitLab"'
    And the answer's userFacingHost is '"https://gitlab.example"'
    And the answer's apiHost is '"{gitlabHost}"'
    And the answer's vcsChecks is '"AGGREGATED"'
    When token "h" sends "POST /api/v1/gitlab-integrations/{g}/test" through B
    Then the answer is 'true'
    When token "h" sends "DELETE /api/v1/gitlab-integrations/{g}" through B
    Then it answers 200
    When token "h" sends "GET /api/v1/gitlab-integrations/{g}" through A
    Then it answers a 404 problem "not-found"

  @op:gitlabIntegrationCreate @op:stackCreate @op:stackSyncCommit @case:success
  Scenario: A stack on a subgroup's project tracks its branch through its provider's default
    When token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"{tok}","customInput":{"name":"Main","spaceID":"acme","isDefault":true}}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","namespace":"acme/platform","repository":"infra","branch":"main","provider":"GITLAB","space":"{team}"},"manageState":false}' through B
    Then it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "seed" through A
    When a commit to "main" of the GitLab project "acme/platform/infra" is kept as "pushed"
    And token "h" sends "POST /api/v1/stacks/infra/sync-commit" through B
    Then it answers 200
    And the answer tracks the commit kept as "pushed" at the branch head kept as "pushed"

  @op:gitlabIntegrationCreate @op:gitlabIntegration @op:gitlabIntegrationUpdate @op:gitlabIntegrationDelete @op:gitlabIntegrationTest @case:denial
  Scenario: A reader of a space sees its integrations without their secrets, and changes nothing
    Given the member key is space-reader on the space "{team}"
    When token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"{tok}","customInput":{"name":"Lab","spaceID":"{team}"}}' through A
    Then the answer's id names "g"
    When token "m" sends "GET /api/v1/gitlab-integrations/{g}" through B
    Then it answers 200
    And the answer's webhookSecret is '""'
    And the answer's webhookUrl is '""'
    When token "m" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"{tok}","customInput":{"name":"Mine","spaceID":"{team}"}}' through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PUT /api/v1/gitlab-integrations/{g}" with '{"customInput":{"space":"{team}"}}' through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "DELETE /api/v1/gitlab-integrations/{g}" through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/gitlab-integrations/{g}/test" through B
    Then it answers a 403 problem "forbidden"

  @op:gitlabIntegrationCreate @op:gitlabIntegrationTest @op:gitlabIntegrationUpdate @op:stackSyncCommit @case:failure
  Scenario: A bad host, a refused token, another space and a stack with no integration are refused
    When token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"http://gitlab.example","privateToken":"t","customInput":{"name":"Bad","spaceID":"acme"}}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/apiHost"
    When token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"revoked","customInput":{"name":"Revoked","spaceID":"acme"}}' through B
    Then it answers 201
    And the answer's id names "r"
    When token "h" sends "POST /api/v1/gitlab-integrations/{r}/test" through A
    Then it answers 200
    And the answer is 'false'
    When token "h" sends "PUT /api/v1/gitlab-integrations/{r}" with '{"customInput":{"space":"{team}"}}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/customInput/space"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Orphan","namespace":"acme/platform","repository":"infra","branch":"main","provider":"GITLAB","space":"{team}"},"manageState":false}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/stacks/orphan/sync-commit" through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/vcsIntegrationId"
