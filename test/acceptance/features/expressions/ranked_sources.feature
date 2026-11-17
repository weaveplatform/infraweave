@postgres
Feature: Expression samples and template secrecy use ranked configuration sources
  The environment editor and saved stack templates use the same winning
  sources as a run: the stack, direct contexts by ascending priority and
  slug, then automatic contexts by descending name and slug. A winning
  secret is masked and makes a derived template write-only; a losing
  secret does not hide the winning public value or its derived template.
  ref: decision 0008
  ref: decision 0013
  ref: decision 0016
  ref: decision 0024

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through A
    Then it answers 201

  @op:expressionCheck @op:expressionComplete @op:expressionEvaluate @op:stackConfigAdd @op:stackConfigSet @op:stackConfigElement @case:success
  Scenario Outline: The ranked winner supplies editor samples and the secrecy of newly saved templates
    Given token "h" sends "POST /api/v1/contexts" with '{"name":"<first>","space":"legacy","labels":["autoattach:*"],"configAttachments":[{"id":"SECRET","type":"ENVIRONMENT_VARIABLE","value":"losing-public","writeOnly":false},{"id":"PUBLIC","type":"ENVIRONMENT_VARIABLE","value":"ranked-losing-secret-579","writeOnly":true}]}' through A
    Then it answers 201
    And token "h" sends "POST /api/v1/contexts" with '{"name":"<winner>","space":"legacy",<attachment>,"configAttachments":[{"id":"SECRET","type":"ENVIRONMENT_VARIABLE","value":"ranked-winning-secret-579","writeOnly":true},{"id":"PUBLIC","type":"ENVIRONMENT_VARIABLE","value":"winning-public","writeOnly":false}]}' through B
    Then it answers 201
    And the stack "infra" read through A with token "h" eventually attaches the contexts "<attached>"
    When token "h" sends "POST /api/v1/expressions/check" with '{"surface":"environment","ref":"infra","form":"template","source":"${{ env.SECRET }}-${{ env.PUBLIC }}"}' through B
    Then it answers 200
    And the answer's diagnostics is '[]'
    When token "h" sends "POST /api/v1/expressions/complete" with '{"surface":"environment","ref":"infra","form":"template","source":"${{ env.SE }}","position":{"line":1,"column":11}}' through A
    Then it answers 200
    And the answer completes "SECRET"
    When token "h" sends "POST /api/v1/expressions/evaluate" with '{"surface":"environment","ref":"infra","form":"template","source":"${{ env.SECRET }}-${{ env.PUBLIC }}"}' through B
    Then it answers 200
    And the answer's value is '"*****-winning-public"'
    When token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"SECRET_ECHO","type":"ENVIRONMENT_VARIABLE","value":"${{ env.SECRET }}","writeOnly":false}' through A
    Then it answers 201
    And the answer's writeOnly is 'true'
    When token "h" sends "PUT /api/v1/stacks/infra/configs/PUBLIC_ECHO" with '{"type":"ENVIRONMENT_VARIABLE","value":"${{ env.PUBLIC }}","writeOnly":false}' through B
    Then it answers 201
    And the answer's writeOnly is 'false'
    And the answer's value is '"${{ env.PUBLIC }}"'
    When token "h" sends "GET /api/v1/stacks/infra/config-element?id=SECRET_ECHO" through B
    Then it answers 200
    And the answer's writeOnly is 'true'
    And the answer leaves out "${{ env.SECRET }}"
    When token "h" sends "GET /api/v1/stacks/infra/config-element?id=PUBLIC_ECHO" through A
    Then it answers 200
    And the answer's writeOnly is 'false'
    And the answer's value is '"${{ env.PUBLIC }}"'
    And no answer of the scenario, log line of A or B, audit entry or event holds "ranked-winning-secret-579"
    And no answer of the scenario, log line of A or B, audit entry or event holds "ranked-losing-secret-579"

    Examples:
      | first     | winner | attachment                                           | attached                  |
      | Automatic | Direct | "stackAttachments":[{"stack":"infra","priority":5}] | direct, auto:automatic    |
      | Alpha     | Zulu   | "labels":["autoattach:*"]                            | auto:alpha, auto:zulu     |

  @op:expressionCheck @op:expressionComplete @op:expressionEvaluate @op:stackConfigAdd @op:stackConfigSet @case:denial
  Scenario: Ranked secret sources remain behind the stack read and write permissions
    Given token "h" sends "POST /api/v1/contexts" with '{"name":"Vault","space":"legacy","configAttachments":[{"id":"SECRET","type":"ENVIRONMENT_VARIABLE","value":"ranked-denied-secret-579","writeOnly":true}],"stackAttachments":[{"stack":"infra","priority":5}]}' through B
    Then it answers 201
    When token "m" sends "POST /api/v1/expressions/check" with '{"surface":"environment","ref":"infra","form":"expression","source":"env.SECRET"}' through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/expressions/complete" with '{"surface":"environment","ref":"infra","form":"template","source":"${{ env.SE }}","position":{"line":1,"column":11}}' through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/expressions/evaluate" with '{"surface":"environment","ref":"infra","form":"expression","source":"env.SECRET"}' through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/stacks/infra/configs" with '{"id":"ECHO","type":"ENVIRONMENT_VARIABLE","value":"${{ env.SECRET }}","writeOnly":false}' through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "PUT /api/v1/stacks/infra/configs/ECHO" with '{"type":"ENVIRONMENT_VARIABLE","value":"${{ env.SECRET }}","writeOnly":false}' through A
    Then it answers a 404 problem "not-found"
    Given the member key is space-reader on the space "legacy"
    When token "m" sends "POST /api/v1/expressions/evaluate" with '{"surface":"environment","ref":"infra","form":"expression","source":"env.SECRET"}' through B
    Then it answers 200
    And the answer's value is '"*****"'
    When token "m" sends "POST /api/v1/stacks/infra/configs" with '{"id":"ECHO","type":"ENVIRONMENT_VARIABLE","value":"${{ env.SECRET }}","writeOnly":false}' through A
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_ADD_CONFIG"'
    When token "m" sends "PUT /api/v1/stacks/infra/configs/ECHO" with '{"type":"ENVIRONMENT_VARIABLE","value":"${{ env.SECRET }}","writeOnly":false}' through B
    Then it answers a 403 problem "forbidden"
    And the answer's action is '"STACK_ADD_CONFIG"'
    And no answer of the scenario, log line of A or B, audit entry or event holds "ranked-denied-secret-579"

  @op:expressionEvaluate @op:stackConfigAdd @op:stackConfigSet @case:failure
  Scenario: Evaluation failures stay masked and refused templates leave stored configuration intact
    Given token "h" sends "POST /api/v1/contexts" with '{"name":"Automatic","space":"legacy","labels":["autoattach:*"],"configAttachments":[{"id":"SECRET","type":"ENVIRONMENT_VARIABLE","value":"losing-public","writeOnly":false}]}' through B
    Then it answers 201
    And token "h" sends "POST /api/v1/contexts" with '{"name":"Vault","space":"legacy","configAttachments":[{"id":"SECRET","type":"ENVIRONMENT_VARIABLE","value":"ranked-failed-secret-579","writeOnly":true}],"stackAttachments":[{"stack":"infra","priority":5}]}' through A
    Then it answers 201
    And the stack "infra" read through B with token "h" eventually attaches the contexts "vault, auto:automatic"
    When token "h" sends "POST /api/v1/expressions/evaluate" with '{"surface":"environment","ref":"infra","form":"expression","source":"fail(env.SECRET)"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/source" as "rule"
    And the answer's errors is '[{"pointer":"/source","reason":"rule","message":"*****","range":{"start":{"line":1,"column":5},"end":{"line":1,"column":6}}}]'
    When token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"BAD","type":"ENVIRONMENT_VARIABLE","value":"${{ env.SECRET + env.MISSING }}","writeOnly":false}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/value" as "rule"
    When token "h" sends "GET /api/v1/stacks/infra/config-element?id=BAD" through A
    Then it answers a 404 problem "not-found"
    Given token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"ECHO","type":"ENVIRONMENT_VARIABLE","value":"kept","writeOnly":false}' through B
    Then it answers 201
    When token "h" sends "PUT /api/v1/stacks/infra/configs/ECHO" with '{"type":"ENVIRONMENT_VARIABLE","value":"${{ env.SECRET + env.MISSING }}","writeOnly":false}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/value" as "rule"
    When token "h" sends "GET /api/v1/stacks/infra/config-element?id=ECHO" through B
    Then it answers 200
    And the answer's writeOnly is 'false'
    And the answer's value is '"kept"'
    And no answer of the scenario, log line of A or B, audit entry or event holds "ranked-failed-secret-579"
