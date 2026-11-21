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

  @policy-login-change @op:policyUpdatev2 @case:success
  Scenario: Updating an active LOGIN policy revokes peer sessions across replicas and preserves the editor and another account
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Initial entry","type":"LOGIN","body":"package infraweave\nallow := true\nroles[\"legacy\"][\"space-reader\"] if { true }\n"}' through A
    And it answers 201
    And the answer's id names "guard"
    And token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through A
    And it answers 200
    And the member key is exchanged through B for token "peer"
    And token "peer"'s read of the space "legacy" through A allows exactly the role "space-reader"'s actions
    And the sibling key is exchanged through B for token "s"
    When token "h" sends "PUT /api/v1/policies/{guard}" with '{"name":"Entry now refused","body":"package infraweave\ndeny := true\n"}' through A
    Then it answers 200
    And the viewer through B with token "peer" answers 401 within a second
    And the viewer through A with token "peer" answers 401
    And the viewer through A with token "h" answers 200
    And the viewer through B with token "h" answers 200
    And the viewer through A with token "s" answers 200
    And exchanging the member key through B answers a 403 problem "forbidden"

  @policy-login-change @op:policyCreatev2 @op:policyDelete @case:success
  Scenario: Creating and deleting active LOGIN policies invalidates old peer credentials without ending the editor
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Initial entry","type":"LOGIN","body":"package infraweave\nallow := true\nroles[\"legacy\"][\"space-reader\"] if { true }\n"}' through A
    And it answers 201
    And token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through B
    And it answers 200
    And the member key is exchanged through A for token "peer"
    And the viewer through B with token "peer" answers 200
    When token "h" sends "POST /api/v1/policies" with '{"name":"Another entry rule","type":"LOGIN","body":"package infraweave\nallow := true\n"}' through B
    Then it answers 201
    And the answer's id names "another"
    And the viewer through A with token "peer" answers 401 within a second
    And the viewer through B with token "peer" answers 401
    And the member key is exchanged through B for token "fresh"
    And the viewer through A with token "fresh" answers 200
    When token "h" sends "DELETE /api/v1/policies/{another}" through A
    Then it answers 200
    And the viewer through B with token "fresh" answers 401 within a second
    And the viewer through A with token "fresh" answers 401
    And the viewer through B with token "h" answers 200

  @policy-login-change @op:policyCreatev2 @op:policyUpdatev2 @op:policyDelete @case:success @case:failure
  Scenario: Inactive LOGIN configuration and failed compilation never revoke managed-user sessions
    When token "h" sends "POST /api/v1/policies" with '{"name":"Inactive entry","type":"LOGIN","body":"package infraweave\ndeny := true\n"}' through A
    Then it answers 201
    And the answer's id names "inactive"
    And the viewer through B with token "before" answers 200
    When token "h" sends "PUT /api/v1/policies/{inactive}" with '{"name":"Still inactive","body":"package infraweave\nallow := true\n"}' through B
    Then it answers 200
    And the viewer through A with token "before" answers 200
    When token "h" sends "PUT /api/v1/policies/{inactive}" with '{"name":"Invalid edit","body":"package infraweave\nallow if {\n"}' through A
    Then it answers a 422 problem "validation-failed"
    And the viewer through B with token "before" answers 200
    When token "h" sends "DELETE /api/v1/policies/{inactive}" through B
    Then it answers 200
    And the viewer through A with token "before" answers 200
