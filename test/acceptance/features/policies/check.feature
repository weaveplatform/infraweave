@postgres @policy-check
Feature: Policy editor compilation
  Checks compile through the same bounded policy compiler as saves, without
  persisting a policy or evaluating it. Any authenticated account member can
  check a supplied body; shared expression controls govern the editor endpoint.

  @op:policyCheck @case:success @case:failure
  Scenario Outline: Editor checks and policy saves share compilation and exact diagnostics
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    When policy check '<input>' through A agrees with save through B

    Examples:
      | input |
      | {"type":"PLAN","body":"package infraweave\\nsample := true\\nwarn contains \"review\" if { true }\\n"} |
      | {"type":"PLAN","engineType":"REGO_V0","body":"package infraweave\\nwarn[\"review\"] { true }\\n"} |
      | {"type":"PLAN","engineType":"REGO_V1","body":"package infraweave\\nwarn[\"review\"] { true }\\n"} |
      | {"type":"PLAN","body":"package infraweave\\nwarn contains \"é🦉\" if { missing }\\n"} |
      | {"type":"PLAN","body":"package infraweave\\nwarn contains http.send({\"url\":\"https://invalid.test\"})\\n"} |
      | {"type":"PLAN","body":"package infraweave\\nwarn := true\\n"} |
      | {"type":"PLAN","body":"package infraweave\\nimport rego.v2\\n"} |
      | {"type":"PLAN","body":"package infraweave\\nwarn contains \"bad\" if {\\n"} |

  @op:policyCheck @case:success
  Scenario: Both replicas compile the default V1 and explicit V0 policy dialects
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through B for token "m"
    When token "h" sends "POST /api/v1/policies/check" with '{"type":"PLAN","body":"package infraweave\nwarn contains \"review\" if { true }\n"}' through A
    Then it answers 200
    And the answer's diagnostics is '[]'
    And the policy contract answer has the shared cache and rate headers
    When token "m" sends "POST /api/v1/policies/check" with '{"type":"PLAN","engineType":"REGO_V0","body":"package infraweave\nwarn[\"review\"] { true }\n"}' through B
    Then it answers 200
    And the answer's diagnostics is '[]'

  @op:policyCheck @case:failure
  Scenario: Check and save respect the configured policy source limit
    Given the policy compiler accepts at most 32 source bytes
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    When policy check '{"type":"PLAN","body":"package infraweave\nwarn contains \"review\" if { true }\n"}' through A agrees with save through B

  @op:policyCheck @case:denial
  Scenario: Switching off expressions refuses policy checking
    Given the installation switches off the features "expressions"
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    When token "h" sends "POST /api/v1/policies/check" with '{"type":"PLAN","body":"package infraweave\n"}' through A
    Then it answers a 403 problem "feature-disabled"

  @op:policyCheck @case:failure
  Scenario: Missing credentials and unknown dialects refuse checks
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    When the browser sends "POST /api/v1/policies/check" with '{"type":"PLAN","body":"package infraweave\n"}' through A
    Then it answers a 401 problem "unauthenticated"
    When token "h" sends "POST /api/v1/policies/check" with '{"type":"PLAN","engineType":"REGO_V2","body":"package infraweave\n"}' through B
    Then it answers a 400 problem "invalid-request"
