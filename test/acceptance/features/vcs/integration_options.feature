@postgres @gitlab
Feature: Operator control of multiple VCS integrations
  The operator can disable the non-default integration option while
  retaining each provider's free default in the account's top space.

  # ref: decisions 0007, 0012, 0016, 0021 and 0022
  @op:gitlabIntegrationCreate @op:vcsIntegrations @case:denial
  Scenario: An operator-disabled option refuses omitted, null and explicit non-defaults without storing them
    Given two infraweave servers, A and B, with multiple VCS integrations disabled
    And the home key is exchanged through A for token "h"
    And a GitLab user "dev" with a token kept as "tok"
    When token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"{tok}","customInput":{"name":"Omitted","spaceID":"acme"}}' through A
    Then it answers a 403 problem "feature-disabled"
    And the problem names the feature "multiple-vcs-integrations"
    When token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"{tok}","customInput":{"name":"Null","spaceID":"acme","isDefault":null}}' through B
    Then it answers a 403 problem "feature-disabled"
    When token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"{tok}","customInput":{"name":"Explicit","spaceID":"acme","isDefault":false}}' through A
    Then it answers a 403 problem "feature-disabled"
    When token "h" sends "GET /api/v1/vcs-integrations" through B
    Then it answers 200
    And the answer's items is '[]'
    When token "h" sends "POST /api/v1/gitlab-integrations" with '{"apiHost":"{gitlabHost}","privateToken":"{tok}","customInput":{"name":"Free default","spaceID":"acme","isDefault":true}}' through B
    Then it answers 201
    And the answer's isDefault is 'true'
