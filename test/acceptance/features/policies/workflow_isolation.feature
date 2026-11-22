@policies @postgres @vcs @policy-approval
Feature: Concurrent public reviews keep independent Rego execution state
  Eight separate stack owners allow pairs of concurrent reviews on each replica. Both
  stored dialects reuse helper names and partial objects while
  producing flags derived only from their own run. Private print output must
  remain separate from retained decisions, public responses and process logs.
  ref: decision 0009
  ref: decision 0016

  @op:runReview @case:success
  Scenario: Concurrent programs retain their input, helper and private print state
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a Gitea repository "infra" with the files:
      | main.tf | terraform {} |
    And the head of "main" of "infra" is kept as "seed"
    And token "h" sends "POST /api/v1/policies" with '{"name":"One","type":"APPROVAL","space":"legacy","engineType":"REGO_V1","body":"package infraweave\nidentity[\"owner\"] := \"one\"\nidentity[\"run\"] := input.run.id\napprove if { count(input.reviews.current.approvals) > 0; print(\"private-policy-print-654\", identity) }\nflag contains concat(\":\", [identity.owner,identity.run]) if count(input.reviews.current.approvals) > 0\n"}' through A
    And it answers 201
    And the answer's id names "One"
    And token "h" sends "POST /api/v1/policies" with '{"name":"Two","type":"APPROVAL","space":"legacy","engineType":"REGO_V0","body":"package infraweave\nimport future.keywords\nidentity[\"owner\"] := \"two\"\nidentity[\"run\"] := input.run.id\napprove { count(input.reviews.current.approvals) > 0; print(\"private-policy-print-654\", identity) }\nflag[concat(\":\", [identity.owner,identity.run])] { count(input.reviews.current.approvals) > 0 }\n"}' through B
    And it answers 201
    And the answer's id names "Two"
    When the workflow fixture reviews eight independent runs in concurrent pairs on both replicas
