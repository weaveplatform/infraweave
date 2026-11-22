@postgres @policies
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
    And token "h" pages all captured policy templates through A

  @op:searchPolicyTemplates @op:searchPolicyTemplatesSuggestions @case:success
  Scenario: Type and label filters return the same rows and facet counts
    Then catalogue searches and suggestions agree for supported types and labels through B

  @op:policyTemplate @op:searchPolicyTemplates @op:searchPolicyTemplatesSuggestions @case:denial
  Scenario: Catalogue browsing requires read access to some account space
    Given the member key is exchanged through B for token "m"
    When token "m" sends "GET /api/v1/policy-templates/01HRPFBHH75YQ0XV7CKM8PT9EG" through A
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/policy-templates/search" with '{}' through B
    Then it answers a 403 problem "forbidden"
    When token "m" sends "POST /api/v1/policy-templates/search/suggestions" with '{}' through A
    Then it answers a 403 problem "forbidden"

  @op:policyTemplate @op:searchPolicyTemplates @op:searchPolicyTemplatesSuggestions @case:failure
  Scenario: Unknown templates and unsupported search fields fail without importing anything
    When token "h" sends "GET /api/v1/policy-templates/01JAAAAAAAAAAAAAAAAAAAAAAA" through B
    Then it answers a 404 problem "not-found"
    When token "h" sends "POST /api/v1/policy-templates/search" with '{"predicates":[{"field":"source","constraint":{"stringEquals":["unknown"]}}]}' through A
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "POST /api/v1/policy-templates/search/suggestions" with '{"predicates":[{"field":"type","constraint":{"booleanEquals":[true]}}]}' through B
    Then it answers a 422 problem "validation-failed"
    When token "h" sends "POST /api/v1/policies" with '{"name":"Unknown origin","type":"PLAN","body":"package infraweave\n","policyTemplateULID":"01JAAAAAAAAAAAAAAAAAAAAAAA"}' through A
    Then it answers a 422 problem "validation-failed"

  @op:policyTemplate @op:policyCreatev2 @op:policySimulatev2 @case:success
  Scenario Outline: Imported catalogue bodies keep origin and execute in their declared dialect
    Then template "<slug>" is imported and simulated with '<input>' returning "<field>" as '<want>' through A

    Examples:
      | slug                                | input                                                                                          | field   | want                                                                                         |
      | allowlist-task-commands              | {"run":{"type":"PROPOSED"}}                                                                    | approve | true                                                                                         |
      | warn-on-destroy                     | {"infraweave":{"run":{"type":"DESTROY"}},"terraform":{"resource_changes":[]}}                     | warn    | ["Destroy runs require manual confirmation (0 resources to destroy)"]                         |
      | discord-drift-detection-with-changes | {}                                                                                             | webhook | []                                                                                           |
      | prs-only                            | {"pull_request":null}                                                                          | ignore  | true                                                                                         |
      | trigger-hardcoded-dependencies      | {}                                                                                             | trigger | []                                                                                           |
