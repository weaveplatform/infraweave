@policy-push-workers @postgres @objectstore @github @gitlab @bitbucket-cloud @bitbucket-dc @azure-devops @docker
Feature: Push replacement policies use actual worker plans
  A real Terraform or OpenTofu plan pauses unconfirmed before a later signed
  provider event creates its replacement and discards only the eligible run.
  ref: decision 0008
  ref: decision 0009
  ref: decision 0012
  ref: decision 0016

  Background:
    Given the release indexes serve the recorded fixtures
    And two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And token "h" eventually reads the Terraform versions through A as the fixture's stable releases newest first
    And token "h" eventually reads the OpenTofu versions through B as the fixture's stable releases newest first
    And a certificate signing request for a P-256 key is kept as "csr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"acme","certificateSigningRequest":"{csr}"}' through A
    And it answers 201
    And the answer's id names "pool"
    And token "h" starts the launcher "l" of the pool "pool" with the key of "csr" through A
    And token "h" eventually reads the worker "l" of the pool "pool" as "IDLE" through B

  Scenario Outline: A push discards an actual unconfirmed plan and plans the replacement
    Given a "<provider>" plan-comment stack "infra" using "<vendor>" with labels '[]'
    And token "h" sends "POST /api/v1/stacks/infra/runs" with '{"commitSha":"{seed}"}' through A
    And it answers 200
    And the answer's id names "prior"
    And token "h" eventually reads the run "prior" in the state "UNCONFIRMED" through B
    And token "h" sends "POST /api/v1/policies" with '{"name":"Replace unconfirmed","type":"GIT_PUSH","body":"package infraweave\ntrack := true\npriority := \"high\"\ncancel contains run.id if { run := input.in_progress[_]; run.id == \"{prior}\" }\nflag contains \"pushed\"\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    And token "h" sends "GET /api/v1/policies/{policy}" through A
    And the policy version for "policy" is kept from its ETag
    When the event repository commits "replacement" on "main"
    And the event fixture pushes "replace" from "seed" to "replacement" on "main" with "valid" verification through B and receives 202
    Then event delivery "replace" settles with 1 new runs
    And the "TRACKED" event run on stack "infra" for commit "replacement" is kept as "run"
    And run "prior" records push cancellation in favor of "run" with state "DISCARDED" through A
    And token "h" eventually reads the run "run" in the state "UNCONFIRMED" through B
    And the answer carries initial push evidence for policy "policy"
    And the answer's priorityPreset is '"high"'
    And the event run "run" has a real tool plan through B

    @terraform
    Examples:
      | provider        | vendor    |
      | GitHub          | terraform |
      | GitLab          | terraform |
      | Bitbucket Cloud | terraform |
      | Bitbucket DC    | terraform |
      | Azure DevOps    | terraform |

    @tofu
    Examples:
      | provider        | vendor   |
      | GitHub          | opentofu |
      | GitLab          | opentofu |
      | Bitbucket Cloud | opentofu |
      | Bitbucket DC    | opentofu |
      | Azure DevOps    | opentofu |
