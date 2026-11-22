@postgres @objectstore @policy-login @policy-phase
Feature: Both Rego dialects drive real LOGIN admission and default evidence
  Authored programs exercise helper rules, comprehensions and partial objects/sets.
  Their results grant actual permissions; flags create no run-owned state. The
  key and managed configuration are seeded fixtures, not a live IdP sign-in.
  ref: decisions 0006, 0007, 0009 and 0016; #666

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through B for token "before"

  @op:apiKeyUser @op:policyEvaluationSample @case:success @case:denial
  Scenario Outline: <engine> LOGIN helpers and partial roles authorize the key and retain its default sample
    Given token "h" sends "POST /api/v1/policies" with '{"name":"Dialect admission","type":"LOGIN","engineType":"<engine>","body":"<body>"}' through A
    And it answers 201
    And the answer's id names "guard"
    When token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"LOGIN_POLICIES"}' through B
    Then it answers 200
    And the member key is exchanged through A for token "allowed"
    And token "allowed"'s read of the space "acme" through B allows exactly the role "space-writer"'s actions
    And policy "guard" has 1 committed LOGIN evaluations without run effects
    And token "h" eventually reads the default "<engine>" LOGIN sample of policy "guard" through B
    And token "allowed" cannot open the raw sample of policy "guard" through A
    When token "h" sends "POST /api/v1/account/update-authorization-scheme" with '{"schema":"MANAGED_USERS"}' through A
    Then it answers 200
    When token "h" sends "DELETE /api/v1/policies/{guard}" through B
    Then it answers 200

    Examples:
      | engine  | body |
      | REGO_V0 | package infraweave\\nimport future.keywords\\naccess := {\\"acme\\": {\\"space-writer\\"}}\\nvalid { input.session.login == \\"01KACME000000000000000000A\\" }\\nallow { valid }\\nroles[space][role] { access[space][role]; valid }\\nmessages := [sprintf(\\"%s\\", [x]) \| x := [\\"ci\\", \\"ops\\"][_]]\\nteam[name] { name := messages[_]; valid }\\nflag[\\"login-no-run\\"] { valid }\\n |
      | REGO_V1 | package infraweave\\naccess := {\\"acme\\": {\\"space-writer\\"}}\\nvalid if { input.session.login == \\"01KACME000000000000000000A\\" }\\nallow if valid\\nroles[space] contains role if { access[space][role]; valid }\\nmessages := [sprintf(\\"%s\\", [x]) \| x := [\\"ci\\", \\"ops\\"][_]]\\nteam contains name if { name := messages[_]; valid }\\nflag contains \\"login-no-run\\" if valid\\n |
