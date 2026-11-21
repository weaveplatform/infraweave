@postgres @policy-login
Feature: Account entry evaluates LOGIN policies before issuing authorization
  Organization authentication and managed-user configuration remain independent
  from the account's active strategy. LOGIN uses real OPA decisions, resolves
  grants inside the target account and preserves the root API key exception.
  ref: decisions 0006, 0007 and 0009; #658

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through B for token "before"

  @op:apiKeyUser @case:denial
  Scenario: A denying LOGIN policy refuses a valid non-root API key on both replicas
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Deny entry","type":"LOGIN","body":"package infraweave\ndeny := true\n"}' through A
    And it answers 201
    And token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through B
    And it answers 200
    Then exchanging the member key through A answers a 403 problem "forbidden"
    And exchanging the member key through B answers a 403 problem "forbidden"

  @op:apiKeyUser @case:success
  Scenario: LOGIN grants authorize a non-root key through the actual issued session
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Read legacy","type":"LOGIN","body":"package infraweave\nallow := true\nroles[\"legacy\"][\"space-reader\"] if { true }\n"}' through A
    And it answers 201
    And token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through B
    And it answers 200
    When the member key is exchanged through A for token "allowed"
    Then token "allowed"'s read of the space "legacy" through B allows exactly the role "space-reader"'s actions
    And token "allowed"'s read of the space "acme" through A is not found

  @op:apiKeyUser @case:success
  Scenario: A key with a real root IAM admin binding can enter despite a denying LOGIN policy
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Deny ordinary entry","type":"LOGIN","body":"package infraweave\ndeny := true\n"}' through A
    And it answers 201
    And token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through B
    And it answers 200
    When the existing home key is exchanged without reseeding through A for token "root"
    Then token "root"'s read of the space "acme" through B allows exactly the role "space-admin"'s actions
