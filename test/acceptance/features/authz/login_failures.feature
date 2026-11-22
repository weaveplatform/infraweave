@identity @postgres @policy-login @policy-login-failures
Feature: LOGIN runtime failures deny entry without granting partial access
  Actual stored Rego programs run through the public API key exchange on both
  replicas. Failed evaluations retain metadata evidence without a session,
  account permission grant or run-owned flag.
  ref: decisions 0006, 0007 and 0009; #658

  Background:
    Given the LOGIN policy evaluation budget is 100 ms
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through B for token "before"

  @op:apiKeyUser @case:failure
  Scenario: A malformed V0 LOGIN result cannot grant access or partial flags
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Invalid LOGIN result","type":"LOGIN","engineType":"REGO_V0","body":"package infraweave\nallow := 17\nflag[\"partial-must-not-commit\"] { true }\n"}' through A
    And it answers 201
    And the answer's id names "guard"
    And token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through B
    And it answers 200
    Then exchanging the member key through A answers a 403 problem "forbidden"
    And exchanging the member key through B answers a 403 problem "forbidden"
    And both refused LOGIN exchanges commit "RESULT" evidence without sessions or grants

  @op:apiKeyUser @case:failure
  Scenario: An expensive V1 LOGIN program reaches the actual OPA deadline and denies safely
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Expensive LOGIN","type":"LOGIN","body":"package infraweave\nvalues := numbers.range(0,299)\nallow if { some a in values; some b in values; some c in values; some d in values; a+b+c+d == -1 }\nflag contains \"partial-must-not-commit\" if true\n"}' through B
    And it answers 201
    And the answer's id names "guard"
    And token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through A
    And it answers 200
    Then exchanging the member key through A answers a 403 problem "forbidden"
    And exchanging the member key through B answers a 403 problem "forbidden"
    And both refused LOGIN exchanges commit "TIMEOUT" evidence without sessions or grants
