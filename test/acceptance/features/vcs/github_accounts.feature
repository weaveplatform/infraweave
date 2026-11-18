@postgres @github
Feature: Each account reaches GitHub through its own App
  GITHUB uses the account's default GitHub App integration when a stack
  names none. The stack retains GITHUB and a null integration, so a later
  resolution follows the current default. An account's App reaches only
  its own installations, including when another account names a repository
  or integration it cannot use. A missing default refuses the write before
  it stores anything.

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the sibling key is exchanged through B for token "s"
    And the member key is exchanged through A for token "m"

  @op:githubEnterpriseIntegrationCreate @op:repositories @op:branches @op:stackCreate @op:stackSyncCommit @case:success @case:failure
  Scenario: Two accounts list and resolve only the repositories of their own Apps
    Given a GitHub App "homeApp" installed on the organization "acme"
    And a GitHub App "siblingApp" installed on the organization "acme-dev"
    And a commit to "main" of the GitHub repository "acme/infra" is kept as "homeHead"
    And a commit to "main" of the GitHub repository "acme-dev/infra" is kept as "siblingHead"
    When token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{homeAppID}","privateKey":"{homeAppKey}","customInput":{"name":"Home App","spaceID":"acme","isDefault":true}}' through A
    Then it answers 201
    And the answer's id names "homeIntegration"
    When token "s" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{siblingAppID}","privateKey":"{siblingAppKey}","customInput":{"name":"Sibling App","spaceID":"acme-dev","isDefault":true}}' through B
    Then it answers 201
    And the answer's id names "siblingIntegration"
    When token "h" sends "GET /api/v1/repositories?provider=GITHUB" through B
    Then it answers 200
    And the answer's items is '[{"namespace":"acme","id":"infra"},{"namespace":"acme","id":"web"}]'
    When token "s" sends "GET /api/v1/repositories?provider=GITHUB" through A
    Then it answers 200
    And the answer's items is '[{"namespace":"acme-dev","id":"infra"},{"namespace":"acme-dev","id":"web"}]'
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","namespace":"acme","repository":"infra","branch":"main"},"manageState":false}' through A
    Then it answers 201
    And the answer's provider is '"GITHUB"'
    When token "h" sends "GET /api/v1/stacks/infra" through B
    Then it answers 200
    And the answer leaves out "vcsIntegrationId"
    And token "h" eventually reads the stack "infra" tracking the commit kept as "homeHead" through B
    When token "s" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","namespace":"acme-dev","repository":"infra","branch":"main","provider":"GITHUB"},"manageState":false}' through B
    Then it answers 201
    And token "s" eventually reads the stack "infra" tracking the commit kept as "siblingHead" through A
    When token "h" sends "GET /api/v1/branches?provider=GITHUB&namespace=acme-dev&repository=infra" through B
    Then it answers a 404 problem "not-found"
    When token "s" sends "GET /api/v1/branches?provider=GITHUB&namespace=acme&repository=infra" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Foreign App","namespace":"acme-dev","repository":"infra","branch":"main","provider":"GITHUB","vcsIntegrationId":"{siblingIntegration}"},"manageState":false}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/input/vcsIntegrationId"
    When token "h" sends "GET /api/v1/stacks/foreign-app" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Foreign Repository","namespace":"acme-dev","repository":"infra","branch":"main","provider":"GITHUB"},"manageState":false}' through B
    Then it answers 201
    When token "h" sends "POST /api/v1/stacks/foreign-repository/sync-commit" through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "GET /api/v1/stacks/foreign-repository" through B
    Then the answer leaves out "trackedCommit"

  @op:stackCreate @op:stackUpdate @op:stackPatch @op:stack @case:success
  Scenario: Omitted and explicit GITHUB writes keep the account default implicit
    Given a GitHub App "owned" installed on the organization "acme"
    And a commit to "main" of the GitHub repository "acme/infra" is kept as "head"
    And token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{ownedID}","privateKey":"{ownedKey}","customInput":{"name":"Owned","spaceID":"acme","isDefault":true}}' through A
    And the answer's id names "integration"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","namespace":"acme","repository":"infra","branch":"main"},"manageState":false}' through B
    Then it answers 201
    And the answer's provider is '"GITHUB"'
    When token "h" sends "GET /api/v1/stacks/infra" through A
    Then it answers 200
    And the answer leaves out "vcsIntegrationId"
    And token "h" eventually reads the stack "infra" tracking the commit kept as "head" through A
    When token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","namespace":"acme","repository":"infra","branch":"main","provider":"GITHUB","description":"updated"}' through A
    Then it answers 200
    And the answer's provider is '"GITHUB"'
    And the answer leaves out "vcsIntegrationId"
    When token "h" sends "PATCH /api/v1/stacks/infra" with '{"provider":"GITHUB","description":"patched"}' through B
    Then it answers 200
    And the answer's provider is '"GITHUB"'
    And the answer leaves out "vcsIntegrationId"
    When token "h" sends "PATCH /api/v1/stacks/infra" with '{"vcsIntegrationId":"{integration}"}' through A
    Then it answers 200
    And the answer's provider is '"GITHUB"'
    And the answer's vcsIntegrationId is '"{integration}"'
    When token "h" sends "PATCH /api/v1/stacks/infra" with '{"vcsIntegrationId":null}' through B
    Then it answers 200
    And the answer leaves out "vcsIntegrationId"
    When token "h" sends "POST /api/v1/stacks/infra/sync-commit" through A
    Then it answers 200
    And the answer tracks the commit kept as "head" at the branch head kept as "head"

  @op:stackCreate @op:stackUpdate @op:stackPatch @case:failure @case:success
  Scenario: A missing default refuses every GITHUB write but an owned named App works
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Omitted","repository":"infra","branch":"main"},"manageState":false}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/provider"
    When token "h" sends "GET /api/v1/stacks/omitted" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Explicit","repository":"infra","branch":"main","provider":"GITHUB"},"manageState":false}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/provider"
    When token "h" sends "GET /api/v1/stacks/explicit" through A
    Then it answers a 404 problem "not-found"
    Given token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Kept","repository":"infra","branch":"main","provider":"GIT"},"manageState":false}' through A
    Then it answers 201
    When token "h" sends "PUT /api/v1/stacks/kept" with '{"name":"Kept","repository":"infra","branch":"main","provider":"GITHUB","description":"refused"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/provider"
    When token "h" sends "PATCH /api/v1/stacks/kept" with '{"provider":"GITHUB","description":"refused"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/provider"
    When token "h" sends "GET /api/v1/stacks/kept" through B
    Then the answer's provider is '"GIT"'
    And the answer leaves out "description"
    Given a GitHub App "named" installed on the organization "acme"
    And a commit to "main" of the GitHub repository "acme/infra" is kept as "head"
    And token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{namedID}","privateKey":"{namedKey}","customInput":{"name":"Named","spaceID":"acme"}}' through A
    And the answer's id names "integration"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Named","namespace":"acme","repository":"infra","branch":"main","provider":"GITHUB","vcsIntegrationId":"{integration}"},"manageState":false}' through B
    Then it answers 201
    And the answer's provider is '"GITHUB"'
    And the answer's vcsIntegrationId is '"{integration}"'
    And token "h" eventually reads the stack "named" tracking the commit kept as "head" through A

  @op:stackCreate @op:stackUpdate @op:stackPatch @case:denial
  Scenario: A reader cannot create or change a GITHUB stack
    Given a GitHub App "owned" installed on the organization "acme"
    And token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{ownedID}","privateKey":"{ownedKey}","customInput":{"name":"Owned","spaceID":"acme","isDefault":true}}' through A
    And the member key is space-reader on the space "acme"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","namespace":"acme","repository":"infra","branch":"main","provider":"GITHUB"},"manageState":false}' through A
    Then it answers 201
    When token "m" sends "POST /api/v1/stacks" with '{"input":{"name":"Denied","namespace":"acme","repository":"infra","branch":"main","provider":"GITHUB"},"manageState":false}' through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","namespace":"acme","repository":"infra","branch":"main","provider":"GITHUB"}' through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "PATCH /api/v1/stacks/infra" with '{"provider":"GITHUB","description":"denied"}' through B
    Then it answers a 403 problem "forbidden"

  @op:stackCreate @op:stackSyncCommit @op:githubEnterpriseIntegrationDelete @case:success @case:failure
  Scenario: An implicit GITHUB stack follows a replacement default at resolution time
    Given a GitHub App "firstApp" installed on the organization "acme"
    And a commit to "main" of the GitHub repository "acme/infra" is kept as "firstHead"
    And token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{firstAppID}","privateKey":"{firstAppKey}","customInput":{"name":"First App","spaceID":"acme","isDefault":true}}' through A
    And the answer's id names "firstIntegration"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","namespace":"acme","repository":"infra","branch":"main"},"manageState":false}' through B
    Then it answers 201
    And token "h" eventually reads the stack "infra" tracking the commit kept as "firstHead" through A
    When token "h" sends "DELETE /api/v1/github-enterprise-integrations/{firstIntegration}" through A
    Then it answers 200
    When token "h" sends "POST /api/v1/stacks/infra/sync-commit" through B
    Then it answers a 422 problem "validation-failed"
    And the problem's detail names "no default GitHub App integration"
    Given a GitHub App "nextApp" installed on the organization "acme"
    And a commit to "main" of the GitHub repository "acme/infra" is kept as "nextHead"
    And token "h" sends "POST /api/v1/github-enterprise-integrations" with '{"apiHost":"{githubHost}","appID":"{nextAppID}","privateKey":"{nextAppKey}","customInput":{"name":"Next App","spaceID":"acme","isDefault":true}}' through A
    Then it answers 201
    When token "h" sends "POST /api/v1/stacks/infra/sync-commit" through B
    Then it answers 200
    And the answer tracks the commit kept as "nextHead" at the branch head kept as "nextHead"
    And the answer's provider is '"GITHUB"'
    And the answer leaves out "vcsIntegrationId"
