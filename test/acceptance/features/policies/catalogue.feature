@postgres @policy-phase
Feature: The pinned policy template catalogue is available to authorized authors
  Catalogue membership and bodies come from the pinned MIT source, with LOGIN
  templates retained only in compatibility tests. Import preserves origin.
  ref: decision 0009; #664

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"

  @op:searchPolicyTemplates @case:success
  Scenario: A policy author can page the template catalogue
    When token "h" sends "POST /api/v1/policy-templates/search" with '{"first":1}' through B
    Then it answers 200
