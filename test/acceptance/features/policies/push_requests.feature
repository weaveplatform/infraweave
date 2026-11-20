@policy-push @postgres @objectstore @github @gitlab @bitbucket-cloud @bitbucket-dc @azure-devops
Feature: Native request and comment push policies
  Signed provider deliveries retain the native source, target and actor. Stored
  child jobs evaluate real Rego before atomically creating deduplicated runs.
  Local provider fixtures prove server behavior; POL-18 owns real-provider proof.
  ref: decision 0009
  ref: decision 0012
  ref: decision 0016

  Background:
    Given two infraweave servers, A and B, on one database
    And the home key is exchanged through A for token "h"
    And a certificate signing request for a P-256 key is kept as "csr"
    And token "h" sends "POST /api/v1/worker-pools" with '{"name":"Builders","space":"acme","certificateSigningRequest":"{csr}"}' through A
    And it answers 201
    And the answer's id names "pool"

  Scenario Outline: A native request selects a proposed run and its branch push deduplicates
    Given a "<provider>" plan-comment stack "infra" using "terraform" with labels '[]'
    And token "h" sends "POST /api/v1/policies" with '{"name":"Native request","type":"GIT_PUSH","body":"package infraweave\npropose if { input.push == null; input.pull_request.action == \"opened\"; input.pull_request.head.branch == \"feature\"; input.pull_request.base.branch == \"target-12\" }\npropose if input.push.branch == \"feature\"\nflag contains \"pushed\"\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    And token "h" sends "GET /api/v1/policies/{policy}" through A
    And the policy version for "policy" is kept from its ETag
    When the event fixture sends request "request" through A
    Then event delivery "request" settles with 1 new runs
    And the "PROPOSED" event run on stack "infra" for commit "seed" is kept as "run"
    When token "h" sends "GET /api/v1/runs/{run}" through B
    Then it answers 200
    And the answer carries initial push evidence for policy "policy"
    And the answer's flags is '["pushed"]'
    When the event fixture pushes "branch" from "commentBase" to "seed" on "feature" with "valid" verification through B and receives 202
    Then event delivery "branch" settles with 0 new runs
    And stack "infra" has exactly 1 event runs for commit "seed"

    Examples:
      | provider       |
      | GitHub         |
      | GitLab         |
      | Bitbucket Cloud |
      | Bitbucket DC    |
      | Azure DevOps    |

  Scenario Outline: A comment starts a run only when its native actor and text match
    Given a "<provider>" plan-comment stack "infra" using "terraform" with labels '[]'
    And token "h" sends "POST /api/v1/policies" with '{"name":"Requested plan","type":"GIT_PUSH","body":"package infraweave\npropose if { input.push == null; input.pull_request.action == \"commented\"; input.pull_request.comment == \"plan this commit\"; input.pull_request.action_initiator != input.pull_request.author; input.pull_request.head.branch == \"feature\" }\nflag contains \"pushed\"\n"}' through A
    And it answers 201
    And the answer's id names "policy"
    And token "h" sends "PUT /api/v1/stacks/infra/policies/{policy}" through B
    And it answers 200
    And token "h" sends "GET /api/v1/policies/{policy}" through A
    And the policy version for "policy" is kept from its ETag
    When the event fixture sends request "request" through A
    Then event delivery "request" settles with 0 new runs
    When the event fixture sends comment "comment" through B
    Then event delivery "comment" settles with 1 new runs
    And the "PROPOSED" event run on stack "infra" for commit "seed" is kept as "run"
    When token "h" sends "GET /api/v1/runs/{run}" through A
    Then it answers 200
    And the answer carries initial push evidence for policy "policy"
    And the answer's flags is '["pushed"]'

    Examples:
      | provider        |
      | GitHub          |
      | GitLab          |
      | Bitbucket Cloud |
      | Bitbucket DC    |
      | Azure DevOps    |
