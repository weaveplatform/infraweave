@postgres
Feature: VCS integrations
  An account keeps integrations with its version control providers, at
  most one default per provider, in its top space, and any number in other
  spaces. A caller lists, reads and searches the integrations in the spaces
  it may read; a stack in a space binds one of its provider in that space
  or one it inherits from. The home key is space-admin on the account's
  top space; the member key holds only the bindings a scenario gives it.
  Space team inherits from the top space and space secret does not. The
  scenarios write the integrations as the vcs domain stores them, with
  their secrets sealed.

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Team","description":"","inheritEntities":true,"parentSpace":"acme"}' through A
    And the answer's id is the space "team"
    And token "h" sends "POST /api/v1/spaces" with '{"name":"Secret","description":"","inheritEntities":false,"parentSpace":"acme"}' through B
    And the answer's id is the space "secret"
    And a GITLAB integration "Main" in the space "acme" as the default is kept as "main"
    And a GITLAB integration "Team" in the space "{team}" is kept as "tgl"
    And a GITLAB integration "Secret" in the space "{secret}" is kept as "sgl"
    And a BITBUCKET_CLOUD integration "Cloud" in the space "acme" is kept as "cloud"

  @op:vcsIntegrations @op:vcsIntegration @op:searchVCSIntegrations @op:searchVCSIntegrationsSuggestions @op:attachableVCSIntegrationsForProviderInSpace @case:success
  Scenario: An admin lists, reads, searches and finds the integrations a space's stacks may bind
    When token "h" sends "GET /api/v1/vcs-integrations" through B
    Then it answers 200
    And the answer lists the integrations "{cloud}, {main}, {sgl}, {tgl}"
    When token "h" sends "GET /api/v1/vcs-integrations?limit=2" through A
    Then the answer lists the integrations "{cloud}, {main}"
    When token "h" sends "GET /api/v1/vcs-integrations/{main}?provider=GITLAB" through A
    Then it answers 200
    And the answer's name is '"Main"'
    And the answer's isDefault is 'true'
    And the answer's labels is '["infra"]'
    And the answer's description is '"Main of the team"'
    When token "h" sends "GET /api/v1/attachable-vcs-integrations-for-provider-in-space?id={team}&provider=GITLAB" through B
    Then it answers 200
    And the answer lists the integrations "{main}, {tgl}"
    When token "h" sends "GET /api/v1/attachable-vcs-integrations-for-provider-in-space?id={secret}&provider=GITLAB" through A
    Then the answer lists the integrations "{sgl}"
    When token "h" sends "POST /api/v1/v-cs-integrations/search" with '{"predicates":[{"field":"provider","constraint":{"stringMatches":["BITBUCKET_CLOUD"]}}]}' through B
    Then it answers 200
    And the answer lists the integrations "{cloud}"
    When token "h" sends "POST /api/v1/v-cs-integrations/search/suggestions" with '{"fields":["label"]}' through A
    Then the answer suggests the label values "infra:4" of 4 integrations

  @op:vcsIntegrations @op:vcsIntegration @op:searchVCSIntegrations @op:searchVCSIntegrationsSuggestions @op:attachableVCSIntegrationsForProviderInSpace @op:vcsIntegrationTest @case:denial
  Scenario: A reader of one space sees only its integrations, and one it cannot read does not exist
    Given the member key is space-reader on the space "{secret}"
    When token "m" sends "GET /api/v1/vcs-integrations" through A
    Then it answers 200
    And the answer lists the integrations "{sgl}"
    When token "m" sends "POST /api/v1/v-cs-integrations/search" with '{}' through B
    Then the answer lists the integrations "{sgl}"
    When token "m" sends "POST /api/v1/v-cs-integrations/search/suggestions" with '{"fields":["label"]}' through A
    Then the answer suggests the label values "infra:1" of 1 integration
    When token "m" sends "GET /api/v1/vcs-integrations/{sgl}?provider=GITLAB" through B
    Then it answers 200
    And the answer leaves out "details"
    When token "m" sends "GET /api/v1/vcs-integrations/{main}?provider=GITLAB" through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/vcs-integrations/{main}/test?provider=GITLAB" through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "GET /api/v1/attachable-vcs-integrations-for-provider-in-space?id={team}&provider=GITLAB" through A
    Then it answers a 404 problem "not-found"

  @op:vcsIntegration @op:vcsIntegrationTest @op:vcsIntegrations @op:searchVCSIntegrations @op:attachableVCSIntegrationsForProviderInSpace @case:failure
  Scenario: Another provider, an unknown ID and a bad cursor are refused
    When token "h" sends "GET /api/v1/vcs-integrations/{main}?provider=BITBUCKET_CLOUD" through A
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/vcs-integrations/nope?provider=GITLAB" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/vcs-integrations/{main}/test?provider=BITBUCKET_CLOUD" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "GET /api/v1/vcs-integrations?cursor=nope" through A
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "POST /api/v1/v-cs-integrations/search" with '{"after":"nope"}' through B
    Then it answers a 400 problem "invalid-cursor"
    When token "h" sends "GET /api/v1/attachable-vcs-integrations-for-provider-in-space?id=nowhere&provider=GITLAB" through A
    Then it answers a 404 problem "not-found"

  @op:stackCreate @op:stackUpdate @case:success
  Scenario: A stack binds an integration of its provider that its space reaches, and no other
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","provider":"GITLAB","vcsIntegrationId":"{tgl}","space":"{team}"},"manageState":false}' through A
    Then it answers 201
    And the answer's vcsIntegration is '{"id":"{tgl}"}'
    And the answer's vcsDetached is 'false'
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Far","repository":"infra","branch":"main","provider":"GITLAB","vcsIntegrationId":"{tgl}","space":"{secret}"},"manageState":false}' through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Far","repository":"infra","branch":"main","provider":"GITLAB","vcsIntegrationId":"{cloud}","space":"{team}"},"manageState":false}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "PUT /api/v1/stacks/infra" with '{"name":"Infra","repository":"infra","branch":"main","provider":"GITLAB","vcsIntegrationId":"{main}","space":"{team}"}' through B
    Then it answers 200
    And the answer's vcsIntegration is '{"id":"{main}"}'
