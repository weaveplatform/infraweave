@postgres
Feature: Expressions editor
  The editor endpoints check an expression or a template against a
  surface and one of its contexts, complete it at a cursor and evaluate
  it against the context's sample values. On the environment surface a
  context is a stack, read by whoever reads its space: env holds the
  variables the stack resolves to, a write-only one masked, and a name it
  does not resolve to is an error with its range; stack describes the
  stack. A stack the caller cannot read answers as one that does not
  exist. The home key is space-admin on the account's top space; the
  member key holds only the bindings a scenario gives it. Stack infra
  lives in legacy, with the variable REGION and the write-only TOKEN.

  @op:expressionCheck @op:expressionComplete @op:expressionEvaluate @case:success
  Scenario: A template is checked, completed and evaluated against a stack's environment, its secret masked
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through A
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"REGION","type":"ENVIRONMENT_VARIABLE","value":"eu","writeOnly":false}' through B
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"TOKEN","type":"ENVIRONMENT_VARIABLE","value":"s3cret","writeOnly":true}' through A
    When token "h" sends "POST /api/v1/expressions/check" with '{"surface":"environment","ref":"infra","form":"template","source":"https://${{ env.REGION }}/${{ stack.id }}"}' through A
    Then it answers 200
    And the answer's diagnostics is '[]'
    And the answer's type is '"string"'
    When token "h" sends "POST /api/v1/expressions/complete" with '{"surface":"environment","ref":"infra","form":"template","source":"${{ env.RE }}","position":{"line":1,"column":11}}' through B
    Then it answers 200
    And the answer completes "REGION"
    When token "h" sends "POST /api/v1/expressions/evaluate" with '{"surface":"environment","ref":"infra","form":"template","source":"https://${{ env.REGION }}/${{ stack.id }}"}' through A
    Then it answers 200
    And the answer's value is '"https://eu/infra"'
    When token "h" sends "POST /api/v1/expressions/evaluate" with '{"surface":"environment","ref":"infra","form":"template","source":"Bearer ${{ env.TOKEN }}"}' through B
    Then it answers 200
    And the answer's value is '"Bearer *****"'
    And the answer leaves out "s3cret"
    When token "h" sends "POST /api/v1/expressions/evaluate" with '{"surface":"environment","ref":"infra","form":"expression","source":"env.REGION","sample":{"env":{"REGION":"us"}}}' through A
    Then the answer's value is '"us"'

  @op:expressionCheck @op:expressionComplete @op:expressionEvaluate @case:denial
  Scenario: A stack in a space the caller cannot read answers 404 until the caller reads the space
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And the member key is exchanged through A for token "m"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through A
    When token "m" sends "POST /api/v1/expressions/check" with '{"surface":"environment","ref":"infra","form":"expression","source":"stack.id"}' through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/expressions/complete" with '{"surface":"environment","ref":"infra","form":"expression","source":"stack.","position":{"line":1,"column":7}}' through B
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/expressions/evaluate" with '{"surface":"environment","ref":"infra","form":"expression","source":"stack.id"}' through A
    Then it answers a 404 problem "not-found"
    When token "m" sends "POST /api/v1/expressions/check" with '{"surface":"environment","ref":"missing","form":"expression","source":"stack.id"}' through B
    Then it answers a 404 problem "not-found"
    Given the member key is space-reader on the space "legacy"
    When token "m" sends "POST /api/v1/expressions/evaluate" with '{"surface":"environment","ref":"infra","form":"expression","source":"stack.id"}' through B
    Then it answers 200
    And the answer's value is '"infra"'

  @op:expressionCheck @op:expressionComplete @op:expressionEvaluate @case:denial
  Scenario: With expressions switched off for the installation, each operation answers 403
    Given the installation switches off the features "expressions"
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    When token "h" sends "POST /api/v1/expressions/check" with '{"surface":"environment","ref":"infra","form":"expression","source":"1"}' through A
    Then it answers a 403 problem "feature-disabled"
    When token "h" sends "POST /api/v1/expressions/complete" with '{"surface":"environment","ref":"infra","form":"expression","source":"1","position":{"line":1,"column":1}}' through B
    Then it answers a 403 problem "feature-disabled"
    When token "h" sends "POST /api/v1/expressions/evaluate" with '{"surface":"environment","ref":"infra","form":"expression","source":"1"}' through A
    Then it answers a 403 problem "feature-disabled"

  @op:expressionCheck @op:expressionComplete @op:expressionEvaluate @case:failure
  Scenario: A misspelt variable is flagged with its range, and a source, cursor or surface the endpoints cannot use is refused
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" sends "POST /api/v1/stacks" with '{"input":{"name":"Infra","repository":"infra","branch":"main","space":"legacy"},"manageState":false}' through A
    And token "h" sends "POST /api/v1/stacks/infra/configs" with '{"id":"REGION","type":"ENVIRONMENT_VARIABLE","value":"eu","writeOnly":false}' through B
    When token "h" sends "POST /api/v1/expressions/check" with '{"surface":"environment","ref":"infra","form":"template","source":"${{ env.REGOIN }}"}' through A
    Then it answers 200
    And the answer flags an error from 1:9 to 1:15
    When token "h" sends "POST /api/v1/expressions/check" with '{"surface":"nowhere","ref":"infra","form":"template","source":"x"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/surface" as "not-found"
    When token "h" sends "POST /api/v1/expressions/complete" with '{"surface":"environment","ref":"infra","form":"template","source":"${{ env.RE }}","position":{"line":2,"column":1}}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/position" as "rule"
    When token "h" sends "POST /api/v1/expressions/evaluate" with '{"surface":"environment","ref":"infra","form":"template","source":"${{ env.REGOIN }}"}' through B
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/source" from 1:9 to 1:15
    When token "h" sends "POST /api/v1/expressions/evaluate" with '{"surface":"environment","ref":"infra","form":"expression","source":"fail(\"no region\")"}' through A
    Then it answers a 422 problem "validation-failed"
    And the problem refuses the field "/source" as "rule"
