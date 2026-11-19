@postgres @vcs
Feature: Operator control of VCS integration access
  Disabling VCS integrations denies authenticated reads and searches after
  establishing access to existing integration data, on either API replica.
  ref: decisions 0012, 0016

  @op:vcsIntegrationSearchUsersSuggestions @op:vcsIntegrationSearchUsers @op:vcsIntegration @op:searchVCSIntegrations @op:vcsIntegrationTest @op:branches @op:repositories @case:denial
  Scenario: Operator-disabled VCS integrations refuse authenticated reads and searches
    Given two infraweave servers, A and B, with VCS integrations disabled
    And the home key is exchanged through A for token "h"
    And a GITLAB integration "Main" in the space "acme" as the default is kept as "main"
    When token "h" sends "GET /api/v1/vcs-integrations/{main}?provider=GITLAB" through A
    Then it answers a 403 problem "feature-disabled"
    And the problem names the feature "vcs-integrations"
    When token "h" sends "GET /api/v1/vcs-integrations/{main}/test?provider=GITLAB" through B
    Then it answers a 403 problem "feature-disabled"
    When token "h" sends "POST /api/v1/v-cs-integrations/search" with '{}' through A
    Then it answers a 403 problem "feature-disabled"
    When token "h" sends "GET /api/v1/repositories?provider=GITLAB" through B
    Then it answers a 403 problem "feature-disabled"
    When token "h" sends "GET /api/v1/branches?provider=GITLAB&namespace=acme%2Fplatform&repository=infra" through A
    Then it answers a 403 problem "feature-disabled"
    When token "h" sends "GET /api/v1/vcs-integrations/{main}/search-users?input=%7B%7D" through B
    Then it answers a 403 problem "feature-disabled"
    When token "h" sends "GET /api/v1/vcs-integrations/{main}/search-users-suggestions?input=%7B%22fields%22%3A%5B%22name%22%5D%7D" through A
    Then it answers a 403 problem "feature-disabled"
